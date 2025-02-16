package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/joho/godotenv"
	"github.com/jordan-wright/email"
	"golang.org/x/oauth2"
)

// Global variable to hold the fallback email domain.
var fallbackEmailDomain string

// printBanner prints a beautified header banner for the tool.
func printBanner() {
	banner := `
#############################################################
#                                                           #
#                   stale-pr-bot by kd_20                   #
#                                                           #
############################################################-
`
	fmt.Println(banner)
}

func main() {
	printBanner()

	// Load .env file (if available)
	if err := godotenv.Load(); err != nil {
		log.Printf("No .env file found or error loading it: %v", err)
	}

	// Get defaults from environment variables (if available)
	defaultGithubToken := os.Getenv("GITHUB_TOKEN")
	defaultGithubBaseURL := os.Getenv("GITHUB_BASE_URL")
	defaultOwner := os.Getenv("GITHUB_OWNER")
	defaultRepo := os.Getenv("GITHUB_REPO")
	defaultDaysInactive := 0
	if v := os.Getenv("DAYS_INACTIVE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			defaultDaysInactive = n
		}
	}
	defaultWarningPeriod := 0
	if v := os.Getenv("WARNING_PERIOD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			defaultWarningPeriod = n
		}
	}
	defaultSMTPServer := os.Getenv("SMTP_SERVER")
	defaultSMTPPort := 587
	if v := os.Getenv("SMTP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			defaultSMTPPort = n
		}
	}
	defaultSMTPUser := os.Getenv("SMTP_USER")
	defaultSMTPPassword := os.Getenv("SMTP_PASSWORD")
	// New default for the email fallback domain.
	defaultEmailDomain := os.Getenv("EMAIL_DOMAIN")
	if defaultEmailDomain == "" {
		defaultEmailDomain = "example.com"
	}

	// Define command-line flags.
	githubTokenFlag := flag.String("github-token", defaultGithubToken, "GitHub API token")
	githubBaseURLFlag := flag.String("github-base-url", defaultGithubBaseURL, "GitHub API base URL (e.g. https://api.github.com/)")
	ownerFlag := flag.String("owner", defaultOwner, "GitHub repository owner")
	repoFlag := flag.String("repo", defaultRepo, "GitHub repository name")
	daysInactiveFlag := flag.Int("days-inactive", defaultDaysInactive, "Number of days to consider a PR stale")
	warningPeriodFlag := flag.Int("warning-period", defaultWarningPeriod, "Warning period in days before closing stale PR")
	smtpServerFlag := flag.String("smtp-server", defaultSMTPServer, "SMTP server address")
	smtpPortFlag := flag.Int("smtp-port", defaultSMTPPort, "SMTP server port")
	smtpUserFlag := flag.String("smtp-user", defaultSMTPUser, "SMTP username")
	smtpPasswordFlag := flag.String("smtp-password", defaultSMTPPassword, "SMTP password")
	emailDomainFlag := flag.String("email-domain", defaultEmailDomain, "Fallback email domain (used when GitHub user's public email is unavailable)")
	flag.Parse()

	// Set the fallback email domain globally.
	fallbackEmailDomain = *emailDomainFlag

	// Simple sanity check.
	if *githubTokenFlag == "" || *githubBaseURLFlag == "" || *ownerFlag == "" || *repoFlag == "" || *daysInactiveFlag <= 0 ||
		*warningPeriodFlag <= 0 || *smtpServerFlag == "" || *smtpUserFlag == "" || *smtpPasswordFlag == "" {
		log.Fatal("Missing required parameter. Please ensure all required flags or environment variables are set.")
	}

	fmt.Println("-------------------------------------------------------------")
	fmt.Println("Starting the stale PR bot in production mode...")
	fmt.Println("-------------------------------------------------------------")

	// Create GitHub client.
	fmt.Println("Creating GitHub client...")
	client, err := getGithubClient(*githubTokenFlag, *githubBaseURLFlag)
	if err != nil {
		log.Fatalf("Error creating GitHub client: %v", err)
	}
	fmt.Println("GitHub client created successfully.")

	// Test GitHub connection.
	fmt.Println("-------------------------------------------------------------")
	fmt.Println("Testing GitHub connection...")
	err = testGitHubConnection(client)
	if err != nil {
		log.Fatalf("GitHub connection test failed: %v", err)
	}
	fmt.Println("GitHub connection successful.")
	fmt.Println("-------------------------------------------------------------")

	// Get open PRs.
	fmt.Println("Fetching open PRs...")
	openPRs, err := getOpenPRs(client, *ownerFlag, *repoFlag)
	if err != nil {
		log.Fatalf("Error fetching PRs: %v", err)
	}
	fmt.Printf("Found %d open PR(s).\n", len(openPRs))
	fmt.Println("-------------------------------------------------------------")
	if len(openPRs) == 0 {
		fmt.Println("No open PRs found.")
		return
	}

	staleDuration := time.Duration(*daysInactiveFlag) * 24 * time.Hour
	staleCutoff := time.Now().Add(-staleDuration)

	// Process PRs.
	for _, pr := range openPRs {
		fmt.Printf("\n-------------------------------------------------------------\n")
		fmt.Printf("Processing PR #%d: %s\n", pr.GetNumber(), pr.GetTitle())
		fmt.Println("-------------------------------------------------------------")

		// Check if PR has 'do not stale' label.
		if hasLabel(pr, "do not stale") {
			fmt.Printf("PR #%d has 'do not stale' label.\n", pr.GetNumber())
			if hasLabel(pr, "stale-warning") {
				fmt.Printf("Removing 'stale-warning' label from PR #%d.\n", pr.GetNumber())
				err = removeLabel(client, *ownerFlag, *repoFlag, pr.GetNumber(), "stale-warning")
				if err != nil {
					fmt.Printf("Error removing label from PR #%d: %v\n", pr.GetNumber(), err)
				} else {
					fmt.Printf("Removed 'stale-warning' label from PR #%d.\n", pr.GetNumber())
				}
			}
			continue
		}

		// Check if PR is stale.
		if pr.GetUpdatedAt().Time.Before(staleCutoff) {
			fmt.Printf("PR #%d is stale.\n", pr.GetNumber())
			if hasLabel(pr, "stale-warning") {
				fmt.Printf("PR #%d already has a 'stale-warning' label.\n", pr.GetNumber())
				// Check if warning period has passed.
				if timeSinceLabel(pr) > time.Duration(*warningPeriodFlag)*24*time.Hour {
					fmt.Printf("Closing PR #%d as it has been inactive after the warning period.\n", pr.GetNumber())
					err := closePR(client, *ownerFlag, *repoFlag, pr.GetNumber())
					if err != nil {
						fmt.Printf("Error closing PR #%d: %v\n", pr.GetNumber(), err)
					} else {
						fmt.Printf("Closed PR #%d.\n", pr.GetNumber())
						// Notify PR author of closure.
						err = notifyPRClosure(pr, *smtpServerFlag, *smtpPortFlag, *smtpUserFlag, *smtpPasswordFlag)
						if err != nil {
							fmt.Printf("Error sending closure email for PR #%d: %v\n", pr.GetNumber(), err)
						} else {
							fmt.Printf("Sent closure notification for PR #%d.\n", pr.GetNumber())
						}
					}
				} else {
					fmt.Printf("PR #%d is still within the warning period.\n", pr.GetNumber())
				}
			} else {
				fmt.Printf("Sending warning for PR #%d.\n", pr.GetNumber())
				err := warnPRAuthor(pr, *smtpServerFlag, *smtpPortFlag, *smtpUserFlag, *smtpPasswordFlag)
				if err != nil {
					fmt.Printf("Error sending email for PR #%d: %v\n", pr.GetNumber(), err)
				} else {
					fmt.Printf("Sent warning for PR #%d.\n", pr.GetNumber())
					err = addWarningLabel(client, *ownerFlag, *repoFlag, pr.GetNumber())
					if err != nil {
						fmt.Printf("Error adding label to PR #%d: %v\n", pr.GetNumber(), err)
					}
				}
			}
		} else {
			fmt.Printf("PR #%d is active.\n", pr.GetNumber())
			// Optionally remove 'stale-warning' label if PR is active.
			if hasLabel(pr, "stale-warning") {
				fmt.Printf("Removing 'stale-warning' label from active PR #%d.\n", pr.GetNumber())
				err = removeLabel(client, *ownerFlag, *repoFlag, pr.GetNumber(), "stale-warning")
				if err != nil {
					fmt.Printf("Error removing label from PR #%d: %v\n", pr.GetNumber(), err)
				} else {
					fmt.Printf("Removed 'stale-warning' label from PR #%d.\n", pr.GetNumber())
				}
			}
		}
	}
}

func testGitHubConnection(client *github.Client) error {
	ctx := context.Background()
	user, _, err := client.Users.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to retrieve authenticated user: %v", err)
	}
	fmt.Printf("Authenticated as GitHub user: %s\n", user.GetLogin())
	return nil
}

func getGithubClient(token, baseURL string) (*github.Client, error) {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Parse and set BaseURL.
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %v", err)
	}
	client.BaseURL = parsedBaseURL

	uploadURLStr := baseURL + "upload/"
	parsedUploadURL, err := url.Parse(uploadURLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid upload URL: %v", err)
	}
	client.UploadURL = parsedUploadURL

	return client, nil
}

func getOpenPRs(client *github.Client, owner, repo string) ([]*github.PullRequest, error) {
	opts := &github.PullRequestListOptions{
		State:       "open",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	ctx := context.Background()
	var allPRs []*github.PullRequest

	for {
		fmt.Println("Fetching pull requests...")
		prs, resp, err := client.PullRequests.List(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("error listing PRs: %v", err)
		}
		allPRs = append(allPRs, prs...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	fmt.Printf("Total open PRs fetched: %d\n", len(allPRs))
	return allPRs, nil
}

func hasLabel(pr *github.PullRequest, labelName string) bool {
	for _, label := range pr.Labels {
		if strings.EqualFold(label.GetName(), labelName) {
			return true
		}
	}
	return false
}

func timeSinceLabel(pr *github.PullRequest) time.Duration {
	return time.Since(pr.GetUpdatedAt().Time)
}

func closePR(client *github.Client, owner, repo string, prNumber int) error {
	ctx := context.Background()
	state := "closed"
	pr := &github.PullRequest{State: &state}
	_, _, err := client.PullRequests.Edit(ctx, owner, repo, prNumber, pr)
	return err
}

func addWarningLabel(client *github.Client, owner, repo string, prNumber int) error {
	ctx := context.Background()
	labels := []string{"stale-warning"}
	_, _, err := client.Issues.AddLabelsToIssue(ctx, owner, repo, prNumber, labels)
	return err
}

func removeLabel(client *github.Client, owner, repo string, prNumber int, labelName string) error {
	ctx := context.Background()
	_, err := client.Issues.RemoveLabelForIssue(ctx, owner, repo, prNumber, labelName)
	return err
}

func warnPRAuthor(pr *github.PullRequest, smtpServer string, smtpPort int, smtpUser, smtpPassword string) error {
	emailAddress := getEmailFromGitHubUser(pr.GetUser())
	if emailAddress == "" {
		fmt.Printf("Email could not be determined for user %s\n", pr.GetUser().GetLogin())
		return nil // Alternatively, handle this case as needed.
	}

	fmt.Printf("Sending warning email to %s for PR #%d.\n", emailAddress, pr.GetNumber())
	prLink := pr.GetHTMLURL()
	subject := fmt.Sprintf("Your pull request #%d is stale", pr.GetNumber())
	body := fmt.Sprintf(`Hello %s,

Your pull request #%d has been inactive for a while. Please update it within the next few days, or it may be closed.

PR Link: %s

Best regards,
The Bot`, pr.GetUser().GetLogin(), pr.GetNumber(), prLink)

	return sendEmail(emailAddress, subject, body, smtpServer, smtpPort, smtpUser, smtpPassword)
}

func notifyPRClosure(pr *github.PullRequest, smtpServer string, smtpPort int, smtpUser, smtpPassword string) error {
	emailAddress := getEmailFromGitHubUser(pr.GetUser())
	if emailAddress == "" {
		fmt.Printf("Email could not be determined for user %s\n", pr.GetUser().GetLogin())
		return nil
	}

	fmt.Printf("Sending closure notification email to %s for PR #%d.\n", emailAddress, pr.GetNumber())
	prLink := pr.GetHTMLURL()
	subject := fmt.Sprintf("Your pull request #%d has been closed", pr.GetNumber())
	body := fmt.Sprintf(`Hello %s,

Your pull request #%d has been closed due to inactivity.

PR Link: %s

If you wish to continue working, please feel free to reopen it or submit a new pull request.

Best regards,
The Bot`, pr.GetUser().GetLogin(), pr.GetNumber(), prLink)

	return sendEmail(emailAddress, subject, body, smtpServer, smtpPort, smtpUser, smtpPassword)
}

func sendEmail(toEmail, subject, body, smtpServer string, smtpPort int, smtpUser, smtpPassword string) error {
	e := email.NewEmail()
	e.From = smtpUser
	e.To = []string{toEmail}
	e.Subject = subject
	e.Text = []byte(body)

	auth := smtp.PlainAuth("", smtpUser, smtpPassword, smtpServer)
	addr := fmt.Sprintf("%s:%d", smtpServer, smtpPort)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %v", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, smtpServer)
	if err != nil {
		return fmt.Errorf("failed to create SMTP client: %v", err)
	}
	defer client.Quit()

	if ok, _ := client.Extension("STARTTLS"); ok {
		config := &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         smtpServer,
		}
		if err = client.StartTLS(config); err != nil {
			return fmt.Errorf("failed to initiate STARTTLS: %v", err)
		}
	} else {
		fmt.Println("SMTP server does not support STARTTLS")
	}

	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("failed to authenticate: %v", err)
	}

	if err = client.Mail(e.From); err != nil {
		return fmt.Errorf("failed to set sender: %v", err)
	}
	if err = client.Rcpt(toEmail); err != nil {
		return fmt.Errorf("failed to set recipient: %v", err)
	}

	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to send data command: %v", err)
	}

	emailBytes, err := e.Bytes()
	if err != nil {
		return fmt.Errorf("failed to generate email bytes: %v", err)
	}

	_, err = wc.Write(emailBytes)
	if err != nil {
		return fmt.Errorf("failed to write email body: %v", err)
	}
	err = wc.Close()
	if err != nil {
		return fmt.Errorf("failed to close email body writer: %v", err)
	}

	return nil
}

func getEmailFromGitHubUser(user *github.User) string {
	email := user.GetEmail()
	if email != "" {
		fmt.Printf("Found public email '%s' for user '%s'.\n", email, user.GetLogin())
		return email
	}
	// Use the fallback email domain set from the flag.
	username := strings.ToLower(user.GetLogin())
	email = fmt.Sprintf("%s@%s", username, fallbackEmailDomain)
	fmt.Printf("Constructed email '%s' for user '%s'.\n", email, user.GetLogin())
	return email
}
