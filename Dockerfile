# Use an official Go runtime as a parent image
FROM golang:1.25-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod ./



# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code from the current directory to the working directory
COPY . .

# Build the Go app
RUN go build -o main .

# Use a minimal alpine image for the final image
FROM alpine:latest

# Set the working directory inside the container
WORKDIR /app

# Copy the built executable from the builder stage
COPY --from=builder /app/main .

# Copy web files
COPY web/ ./web/

# Expose the port the app listens on
EXPOSE 8080

# Run the executable
CMD ["./main"]
