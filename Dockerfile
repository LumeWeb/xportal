# Use the official Golang image as the base image for the builder stage
FROM golang:1.22.1 as go-builder

# Override the default shell to use bash
SHELL ["bash", "-c"]

# Set the working directory inside the container
WORKDIR /xportal

# Copy the entire current directory to the container
COPY . .

# Copy the .git directory to enable Git operations
COPY ./.git ./.git

# Build the Go application
RUN make

# Use a lightweight base image for the final stage
FROM golang:1.22.1 as final

# Update the package lists and install CA certificates
RUN apt-get update && apt-get install -y ca-certificates && apt-get clean


# Copy the built binary from the go-builder stage
COPY --from=go-builder /xportal/xportal /usr/local/bin/xportal