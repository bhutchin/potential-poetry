#!/bin/bash
# This script generates a self-signed SSL certificate for local development.

# Exit immediately if a command exits with a non-zero status.
set -e

# Define certificate details
DOMAIN="localhost"
CERT_DIR="certs"
KEY_FILE="${CERT_DIR}/key.pem"
CERT_FILE="${CERT_DIR}/cert.pem"
DAYS_VALID=365

# Check if OpenSSL is installed
if ! [ -x "$(command -v openssl)" ]; then
  echo 'Error: openssl is not installed.' >&2
  exit 1
fi

# Create certs directory if it doesn't exist
mkdir -p "$CERT_DIR"

# Generate the certificate if it doesn't exist
if [ -f "$CERT_FILE" ] && [ -f "$KEY_FILE" ]; then
  echo "SSL certificate already exists in '${CERT_DIR}'. Skipping generation."
else
  echo "Generating self-signed SSL certificate for ${DOMAIN}..."
  openssl req -x509 -newkey rsa:4096 -keyout "$KEY_FILE" -out "$CERT_FILE" \
    -sha256 -days $DAYS_VALID -nodes \
    -subj "/C=US/ST=Local/L=City/O=Development/CN=${DOMAIN}"
  echo "Certificate generated successfully in the '${CERT_DIR}' directory."
fi