#!/usr/bin/env bash
# One-time setup of the production API on Azure Container Apps (DECISIONS #34). Safe to re-run:
# each step creates what's missing and updates what exists.
#
#   az login                       # first, as the account that owns the subscription
#   scripts/azure-setup.sh app     # resource group, Container Apps environment, the API app
#   scripts/azure-setup.sh github  # OIDC deploy identity for GitHub Actions + repo secrets
#
# Reads DATABASE_URL and REDIS_URL from .env (git-ignored) and never prints them. Afterwards,
# every push to main that passes CI deploys a new revision (.github/workflows/ci.yml).
set -euo pipefail
cd "$(dirname "$0")/.."

RG=passable-rg
LOCATION=${LOCATION:-eastus} # next to Neon and Upstash (us-east-1)
ENV_NAME=passable-env
APP=passable-api
IMAGE=${IMAGE:-ghcr.io/prateekgithub23/passable-api:latest}
REPO=PrateekGithub23/passable
SITE=https://passable-alpha.vercel.app

step() { printf '\n==> %s\n' "$*"; }

provision_app() {
  set -a; . ./.env; set +a # DATABASE_URL, REDIS_URL

  step "Resource providers, resource group, environment"
  az provider register -n Microsoft.App --wait -o none
  az group create -n "$RG" -l "$LOCATION" -o none
  # No Log Analytics workspace: it bills per GB ingested. Console logs can still be streamed
  # live with `az containerapp logs show -n passable-api -g passable-rg --follow`.
  az containerapp env show -n "$ENV_NAME" -g "$RG" -o none 2>/dev/null ||
    az containerapp env create -n "$ENV_NAME" -g "$RG" -l "$LOCATION" --logs-destination none -o none
  env_id=$(az containerapp env show -n "$ENV_NAME" -g "$RG" --query id -o tsv)

  step "Container App $APP"
  # The app's full spec, as JSON (which is valid YAML). Written to a private temp file because
  # it contains the database and Redis URLs; deleted on exit.
  spec=$(mktemp)
  trap 'rm -f "$spec"' EXIT
  chmod 600 "$spec"
  ENV_ID=$env_id LOCATION=$LOCATION IMAGE=$IMAGE SITE=$SITE python3 - >"$spec" <<'PY'
import json, os
e = os.environ
print(json.dumps({
  "location": e["LOCATION"],
  "properties": {
    "managedEnvironmentId": e["ENV_ID"],
    "configuration": {
      "activeRevisionsMode": "Single",
      "ingress": {"external": True, "targetPort": 8080, "transport": "auto", "allowInsecure": False},
      "secrets": [
        {"name": "database-url", "value": e["DATABASE_URL"]},
        {"name": "redis-url", "value": e["REDIS_URL"]},
      ],
    },
    "template": {
      "containers": [{
        "name": "api",
        "image": e["IMAGE"],
        # The graph is ~9 MiB of heap; 0.25 vCPU / 0.5 GiB is the smallest size.
        "resources": {"cpu": 0.25, "memory": "0.5Gi"},
        "env": [
          {"name": "DATABASE_URL", "secretRef": "database-url"},
          {"name": "REDIS_URL", "secretRef": "redis-url"},
          {"name": "CORS_ORIGINS", "value": e["SITE"] + ",http://localhost:3000"},
          # Expiry and catch-up every 10 min, so the free-tier Postgres can suspend when idle.
          {"name": "EXPIRY_INTERVAL", "value": "10m"},
          # Container Apps' proxy (Envoy) appends the client's address to X-Forwarded-For.
          {"name": "TRUSTED_IP_HEADER", "value": "X-Forwarded-For"},
        ],
        # Same probe design as deploy/k8s: readiness pings the database, liveness doesn't, so a
        # database outage takes the replica out of rotation without restarting it.
        "probes": [
          {"type": "Startup", "httpGet": {"path": "/healthz", "port": 8080}, "periodSeconds": 2, "failureThreshold": 30},
          {"type": "Readiness", "httpGet": {"path": "/healthz", "port": 8080}, "periodSeconds": 10, "timeoutSeconds": 3},
          {"type": "Liveness", "tcpSocket": {"port": 8080}, "periodSeconds": 20},
        ],
      }],
      # Scale to zero when idle (no requests, no open WebSockets), at most one replica: the
      # free monthly allowance covers a small app that sleeps most of the time.
      "scale": {"minReplicas": 0, "maxReplicas": 1,
                "rules": [{"name": "http", "http": {"metadata": {"concurrentRequests": "50"}}}]},
    },
  },
}))
PY
  if az containerapp show -n "$APP" -g "$RG" -o none 2>/dev/null; then
    az containerapp update -n "$APP" -g "$RG" --yaml "$spec" -o none
  else
    az containerapp create -n "$APP" -g "$RG" --yaml "$spec" -o none
  fi
  fqdn=$(az containerapp show -n "$APP" -g "$RG" --query properties.configuration.ingress.fqdn -o tsv)
  echo "API: https://$fqdn"
  curl -fsS --retry 10 --retry-delay 6 --retry-all-errors "https://$fqdn/healthz" && echo
}

setup_github() {
  step "Deploy identity for GitHub Actions (OIDC, no stored password)"
  sub=$(az account show --query id -o tsv)
  tenant=$(az account show --query tenantId -o tsv)
  name=passable-github-deploy
  app_id=$(az ad app list --display-name "$name" --query "[0].appId" -o tsv)
  [ -n "$app_id" ] || app_id=$(az ad app create --display-name "$name" --query appId -o tsv)
  az ad sp show --id "$app_id" -o none 2>/dev/null || az ad sp create --id "$app_id" -o none
  # Least privilege: Contributor on this resource group only, not the subscription.
  az role assignment create --assignee "$app_id" --role Contributor \
    --scope "/subscriptions/$sub/resourceGroups/$RG" -o none
  # Trust GitHub's OIDC tokens for pushes to main in this repo, and nothing else.
  if ! az ad app federated-credential list --id "$app_id" --query "[?name=='github-main']" -o tsv | grep -q .; then
    az ad app federated-credential create --id "$app_id" --parameters "{
      \"name\": \"github-main\",
      \"issuer\": \"https://token.actions.githubusercontent.com\",
      \"subject\": \"repo:$REPO:ref:refs/heads/main\",
      \"audiences\": [\"api://AzureADTokenExchange\"]
    }" -o none
  fi

  step "Repository secrets"
  gh secret set AZURE_CLIENT_ID -R "$REPO" -b "$app_id"
  gh secret set AZURE_TENANT_ID -R "$REPO" -b "$tenant"
  gh secret set AZURE_SUBSCRIPTION_ID -R "$REPO" -b "$sub"
  # For the migration step; piped so the value never appears in arguments or output.
  set -a; . ./.env; set +a
  printf '%s' "$DATABASE_URL" | gh secret set DATABASE_URL -R "$REPO"
}

case "${1:-}" in
  app) provision_app ;;
  github) setup_github ;;
  *) echo "usage: $0 app|github" >&2; exit 2 ;;
esac
