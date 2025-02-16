# Stage 1: Build the binary
FROM golang:1.20-alpine AS builder

# Install git (required for fetching go modules)
RUN apk add --no-cache git

WORKDIR /app

# Copy go module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the remaining source code
COPY . .

# Build the Go binary (adjust the output name as needed)
RUN CGO_ENABLED=0 GOOS=linux go build -o stale-pr-bot

# Stage 2: Create a minimal image
FROM alpine:latest

WORKDIR /app

# Copy the compiled binary from the builder stage
COPY --from=builder /app/stale-pr-bot .

# Set the entrypoint to run your application
ENTRYPOINT ["./stale-pr-bot"]
