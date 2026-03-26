#!/usr/bin/env bash
set -euo pipefail
APP_NAME="fglpkg-registry"
REGION="lhr"
VOLUME_NAME="registry_meta"
R2_BUCKET="fglpkg-packages"

echo "fglpkg registry - Fly.io bootstrap"
fly apps create "$APP_NAME" --org personal 2>/dev/null || echo "(app already exists)"
fly volumes create "$VOLUME_NAME" --app "$APP_NAME" --size 1 --region "$REGION" 2>/dev/null || echo "(volume already exists)"

ADMIN_TOKEN=$(openssl rand -hex 32)
echo "Admin token (save - shown only once): $ADMIN_TOKEN"

read -rp "Cloudflare Account ID: " CF_ACCOUNT_ID
read -rp "R2 Access Key ID: " R2_KEY_ID
read -rsp "R2 Access Key Secret: " R2_KEY_SECRET; echo
read -rp "R2 Public Bucket URL: " R2_PUBLIC_URL

fly secrets set --app "$APP_NAME" \
  "FGLPKG_PUBLISH_TOKEN=$ADMIN_TOKEN" \
  "R2_ACCOUNT_ID=$CF_ACCOUNT_ID" \
  "R2_ACCESS_KEY_ID=$R2_KEY_ID" \
  "R2_ACCESS_KEY_SECRET=$R2_KEY_SECRET" \
  "R2_BUCKET_NAME=$R2_BUCKET" \
  "R2_PUBLIC_BUCKET_URL=$R2_PUBLIC_URL"

fly deploy --app "$APP_NAME" --remote-only
FLY_TOKEN=$(fly tokens create deploy --app "$APP_NAME" --expiry 8760h)
echo "GitHub Actions secret (FLY_API_TOKEN): $FLY_TOKEN"
echo "Setup complete! Registry: https://$APP_NAME.fly.dev"
