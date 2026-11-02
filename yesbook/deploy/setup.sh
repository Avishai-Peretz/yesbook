#!/usr/bin/env bash
# One-time setup of Yesbook in a Google Cloud project, sized for the free
# tier: Document API, Content API and outbox publisher on Cloud Run (all
# scale to zero), the processing worker as a Cloud Run Job driven by Cloud
# Workflows, four GCS buckets, a Pub/Sub topic and a Cloud Scheduler sweep.
# Cloud Storage and the e2-micro VM are only free in us-central1, us-east1
# and us-west1.
#
# PostgreSQL, one of:
#   DB_VM=1        creates a private e2-micro PostgreSQL 17 VM in the default
#                  VPC; Cloud Run reaches it through Direct VPC egress.
#   DATABASE_URL   any reachable PostgreSQL (TLS), no VPC involved.
# Gemini: GEMINI_API_KEY uses the Gemini Developer API; without it the worker
# uses Vertex AI with its service account (billed per request).
#
# Usage, from the yesbook directory with Docker, openssl and gcloud:
#   PROJECT=my-project REGION=us-central1 DB_VM=1 \
#   API_KEYS='tenant-a:<16+ char key>' ./deploy/setup.sh
# Set ALERTS=1 to also create the log-based metrics and alert policies.
set -euo pipefail

require_variables() {
  : "${PROJECT:?set PROJECT}" "${REGION:?set REGION}" "${API_KEYS:?set API_KEYS}"
  if [[ "${DB_VM:-0}" != "1" ]]; then : "${DATABASE_URL:?set DATABASE_URL or DB_VM=1}"; fi
  TAG="${TAG:-v1}"
  ZONE="${ZONE:-${REGION}-a}"
  REPO="${REGION}-docker.pkg.dev/${PROJECT}/yesbook"
  WORKFLOW="yesbook-processing"
  TOPIC="yesbook-document-events"
  BUCKET_PREFIX="${BUCKET_PREFIX:-${PROJECT}-yesbook}"
}

enable_apis() {
  echo "== APIs"
  gcloud services enable run.googleapis.com workflows.googleapis.com workflowexecutions.googleapis.com \
    pubsub.googleapis.com storage.googleapis.com secretmanager.googleapis.com artifactregistry.googleapis.com \
    cloudscheduler.googleapis.com iamcredentials.googleapis.com logging.googleapis.com monitoring.googleapis.com \
    compute.googleapis.com aiplatform.googleapis.com --project="${PROJECT}"
}

create_database_vm() {
  VPC=()
  if [[ "${DB_VM:-0}" != "1" ]]; then
    return
  fi
  echo "== PostgreSQL VM (private; a temporary public IP is used for the package install only)"
  VPC=(--network=default --subnet=default --vpc-egress=private-ranges-only)
  gcloud compute addresses create yesbook-db --region="${REGION}" --subnet=default --project="${PROJECT}"
  DB_IP="$(gcloud compute addresses describe yesbook-db --region="${REGION}" --project="${PROJECT}" --format='value(address)')"
  SUBNET_RANGE="$(gcloud compute networks subnets describe default --region="${REGION}" --project="${PROJECT}" --format='value(ipCidrRange)')"
  PASS_FILE="$(mktemp)"
  trap 'rm -f "${PASS_FILE}"' EXIT
  openssl rand -hex 24 | tr -d '\n' > "${PASS_FILE}"
  gcloud compute instances create yesbook-db --zone="${ZONE}" --project="${PROJECT}" \
    --machine-type=e2-micro --image-family=debian-13 --image-project=debian-cloud \
    --boot-disk-size=30GB --boot-disk-type=pd-standard --subnet=default --private-network-ip="${DB_IP}" \
    --no-service-account --no-scopes --shielded-secure-boot --shielded-vtpm --shielded-integrity-monitoring \
    --metadata="yesbook-db-subnet=${SUBNET_RANGE}" \
    --metadata-from-file="startup-script=deploy/db-vm-startup.sh,yesbook-db-password=${PASS_FILE}"
  until gcloud compute instances get-serial-port-output yesbook-db --zone="${ZONE}" --project="${PROJECT}" 2>/dev/null |
    grep -q 'yesbook-db-ready'; do sleep 20; done
  gcloud compute instances remove-metadata yesbook-db --zone="${ZONE}" --project="${PROJECT}" --keys=yesbook-db-password
  gcloud compute instances delete-access-config yesbook-db --zone="${ZONE}" --project="${PROJECT}" \
    --access-config-name=external-nat --quiet
  DATABASE_URL="postgres://yesbook:$(cat "${PASS_FILE}")@${DB_IP}:5432/yesbook?sslmode=require"
}

sa() { echo "yb-$1@${PROJECT}.iam.gserviceaccount.com"; }

grant_secret() {
  gcloud secrets add-iam-policy-binding "$1" --member="serviceAccount:$(sa "$2")" \
    --role=roles/secretmanager.secretAccessor --project="${PROJECT}"
}

create_service_accounts() {
  echo "== Service accounts"
  for sa_name in document-api content-api outbox-publisher worker workflow scheduler; do
    gcloud iam service-accounts create "yb-${sa_name}" --display-name="Yesbook ${sa_name}" --project="${PROJECT}"
  done
}

create_secrets() {
  echo "== Secrets"
  printf '%s' "${DATABASE_URL}" | gcloud secrets create yesbook-database-url --replication-policy=automatic --data-file=- --project="${PROJECT}"
  printf '%s' "${API_KEYS}" | gcloud secrets create yesbook-api-keys --replication-policy=automatic --data-file=- --project="${PROJECT}"
  for s in document-api content-api outbox-publisher worker; do grant_secret yesbook-database-url "$s"; done
  for s in document-api content-api; do grant_secret yesbook-api-keys "$s"; done
}

bind_bucket() {
  gcloud storage buckets add-iam-policy-binding "gs://${BUCKET_PREFIX}-$1" \
    --member="serviceAccount:$(sa "$2")" --role="$3"
}

create_buckets() {
  echo "== Buckets"
  for b in staging originals evidence derived; do
    gcloud storage buckets create "gs://${BUCKET_PREFIX}-${b}" --location="${REGION}" \
      --uniform-bucket-level-access --public-access-prevention --project="${PROJECT}"
  done
  gcloud storage buckets update "gs://${BUCKET_PREFIX}-staging" --lifecycle-file=deploy/staging-lifecycle.json
  bind_bucket staging document-api roles/storage.objectCreator
  bind_bucket staging document-api roles/storage.objectViewer
  bind_bucket staging worker roles/storage.objectViewer
  bind_bucket originals worker roles/storage.objectAdmin
  bind_bucket evidence worker roles/storage.objectAdmin
  bind_bucket derived worker roles/storage.objectAdmin
  bind_bucket originals content-api roles/storage.objectViewer
  bind_bucket derived content-api roles/storage.objectViewer
  # Signed URLs are signed through the IAM Credentials API with the service's
  # own identity.
  for s in document-api content-api; do
    gcloud iam service-accounts add-iam-policy-binding "$(sa "$s")" --project="${PROJECT}" \
      --member="serviceAccount:$(sa "$s")" --role=roles/iam.serviceAccountTokenCreator
  done
}

create_pubsub() {
  echo "== Pub/Sub"
  gcloud pubsub topics create "${TOPIC}" --project="${PROJECT}"
  gcloud pubsub topics add-iam-policy-binding "${TOPIC}" --project="${PROJECT}" \
    --member="serviceAccount:$(sa outbox-publisher)" --role=roles/pubsub.publisher
}

bind_project() {
  gcloud projects add-iam-policy-binding "${PROJECT}" --member="serviceAccount:$(sa "$1")" \
    --role="$2" --condition=None >/dev/null
}

grant_project_roles() {
  echo "== Project roles"
  bind_project document-api roles/workflows.invoker
  bind_project workflow roles/run.viewer
  bind_project workflow roles/logging.logWriter
  gcloud iam service-accounts add-iam-policy-binding "$(sa worker)" --project="${PROJECT}" \
    --member="serviceAccount:$(sa workflow)" --role=roles/iam.serviceAccountUser
}

configure_worker_env() {
  BUCKETS="YESBOOK_BUCKET_STAGING=${BUCKET_PREFIX}-staging,YESBOOK_BUCKET_ORIGINALS=${BUCKET_PREFIX}-originals,YESBOOK_BUCKET_EVIDENCE=${BUCKET_PREFIX}-evidence,YESBOOK_BUCKET_DERIVED=${BUCKET_PREFIX}-derived"
  DB_SECRET="YESBOOK_DATABASE_URL=yesbook-database-url:latest"
  WORKER_SECRETS="${DB_SECRET}"
  WORKER_ENV="${BUCKETS}"
  if [[ -n "${GEMINI_API_KEY:-}" ]]; then
    printf '%s' "${GEMINI_API_KEY}" | gcloud secrets create yesbook-gemini-api-key --replication-policy=automatic --data-file=- --project="${PROJECT}"
    grant_secret yesbook-gemini-api-key worker
    WORKER_SECRETS="${WORKER_SECRETS},YESBOOK_GEMINI_API_KEY=yesbook-gemini-api-key:latest"
  else
    bind_project worker roles/aiplatform.user
    WORKER_ENV="${WORKER_ENV},YESBOOK_PROJECT=${PROJECT},YESBOOK_GEMINI_LOCATION=global"
  fi
}

build_images() {
  echo "== Images (built locally; Artifact Registry keeps the two newest)"
  gcloud artifacts repositories create yesbook --repository-format=docker --location="${REGION}" \
    --disable-vulnerability-scanning --project="${PROJECT}"
  gcloud artifacts repositories set-cleanup-policies yesbook --location="${REGION}" --project="${PROJECT}" \
    --policy=deploy/artifact-cleanup.json --no-dry-run
  gcloud auth configure-docker "${REGION}-docker.pkg.dev" --quiet
  for cmd in document-api content-api outbox-publisher worker migrate; do
    docker build --build-arg CMD="${cmd}" -t "${REPO}/${cmd}:${TAG}" .
    docker push "${REPO}/${cmd}:${TAG}"
  done
  if [[ "${WEB:-0}" == "1" ]]; then
    docker build -f Dockerfile.web -t "${REPO}/web:${TAG}" .
    docker push "${REPO}/web:${TAG}"
  fi
}

apply_schema() {
  echo "== Schema (review migrations/ before this step)"
  gcloud run jobs deploy yesbook-migrate --image="${REPO}/migrate:${TAG}" --region="${REGION}" --project="${PROJECT}" \
    --service-account="$(sa worker)" "${VPC[@]}" --set-secrets="${DB_SECRET}" --max-retries=0 --task-timeout=5m
  gcloud run jobs execute yesbook-migrate --region="${REGION}" --project="${PROJECT}" --wait
}

deploy_worker() {
  echo "== Processing worker and workflow"
  gcloud run jobs deploy yesbook-worker --image="${REPO}/worker:${TAG}" --region="${REGION}" --project="${PROJECT}" \
    --service-account="$(sa worker)" "${VPC[@]}" --tasks=1 --max-retries=0 --task-timeout=30m \
    --cpu=1 --memory=2Gi --set-secrets="${WORKER_SECRETS}" --set-env-vars="${WORKER_ENV}"
  gcloud run jobs add-iam-policy-binding yesbook-worker --region="${REGION}" --project="${PROJECT}" \
    --member="serviceAccount:$(sa workflow)" --role=roles/run.jobsExecutorWithOverrides
  gcloud workflows deploy "${WORKFLOW}" --location="${REGION}" --project="${PROJECT}" --source=deploy/workflow.yaml \
    --service-account="$(sa workflow)"
}

deploy_apis() {
  echo "== APIs"
  gcloud run deploy document-api --image="${REPO}/document-api:${TAG}" --region="${REGION}" --project="${PROJECT}" \
    --service-account="$(sa document-api)" "${VPC[@]}" --allow-unauthenticated --max-instances=2 \
    --set-secrets="${DB_SECRET},YESBOOK_API_KEYS=yesbook-api-keys:latest" \
    --set-env-vars="${BUCKETS},YESBOOK_WORKFLOW=projects/${PROJECT}/locations/${REGION}/workflows/${WORKFLOW}"
  gcloud run deploy content-api --image="${REPO}/content-api:${TAG}" --region="${REGION}" --project="${PROJECT}" \
    --service-account="$(sa content-api)" "${VPC[@]}" --allow-unauthenticated --max-instances=2 \
    --set-secrets="${DB_SECRET},YESBOOK_API_KEYS=yesbook-api-keys:latest" --set-env-vars="${BUCKETS}"
}

deploy_publisher() {
  echo "== Outbox publisher (private; scaled to zero, so the scheduler sweep bounds latency to ~1 minute)"
  gcloud run deploy outbox-publisher --image="${REPO}/outbox-publisher:${TAG}" --region="${REGION}" --project="${PROJECT}" \
    --service-account="$(sa outbox-publisher)" "${VPC[@]}" --no-allow-unauthenticated --max-instances=1 \
    --set-secrets="${DB_SECRET}" --set-env-vars="YESBOOK_PROJECT=${PROJECT},YESBOOK_TOPIC=${TOPIC}"
  gcloud run services add-iam-policy-binding outbox-publisher --region="${REGION}" --project="${PROJECT}" \
    --member="serviceAccount:$(sa scheduler)" --role=roles/run.invoker
  PUBLISHER_URL="$(gcloud run services describe outbox-publisher --region="${REGION}" --project="${PROJECT}" --format='value(status.url)')"
  gcloud scheduler jobs create http yesbook-outbox-sweep --location="${REGION}" --project="${PROJECT}" \
    --schedule="* * * * *" --uri="${PUBLISHER_URL}/sweep" --http-method=POST --attempt-deadline=60s \
    --oidc-service-account-email="$(sa scheduler)" --oidc-token-audience="${PUBLISHER_URL}"
}

# Optional same-origin UI. Set WEB=1 USER_TENANTS='user@domain:tenant' WEB_ORIGIN=https://....
# Enable IAP on the web service afterwards; see docs/production-web.md.
deploy_web() {
  if [[ "${WEB:-0}" != "1" ]]; then
    return
  fi
  : "${USER_TENANTS:?set USER_TENANTS when WEB=1}"
  echo "== Web BFF"
  gcloud iam service-accounts create yb-web --display-name="Yesbook web" --project="${PROJECT}" || true
  printf '%s' "${USER_TENANTS}" | gcloud secrets create yesbook-user-tenants --replication-policy=automatic --data-file=- --project="${PROJECT}" \
    || printf '%s' "${USER_TENANTS}" | gcloud secrets versions add yesbook-user-tenants --data-file=- --project="${PROJECT}"
  grant_secret yesbook-api-keys web
  grant_secret yesbook-user-tenants web
  DOC_URL="$(gcloud run services describe document-api --region="${REGION}" --project="${PROJECT}" --format='value(status.url)')"
  CONTENT_URL="$(gcloud run services describe content-api --region="${REGION}" --project="${PROJECT}" --format='value(status.url)')"
  gcloud run deploy web --image="${REPO}/web:${TAG}" --region="${REGION}" --project="${PROJECT}" \
    --service-account="$(sa web)" --allow-unauthenticated --max-instances=2 \
    --set-secrets="YESBOOK_API_KEYS=yesbook-api-keys:latest,YESBOOK_USER_TENANTS=yesbook-user-tenants:latest" \
    --set-env-vars="YESBOOK_DOCUMENT_API_URL=${DOC_URL},YESBOOK_CONTENT_API_URL=${CONTENT_URL},YESBOOK_AUTH_MODE=iap"
  gcloud run services add-iam-policy-binding document-api --region="${REGION}" --project="${PROJECT}" \
    --member="serviceAccount:$(sa web)" --role=roles/run.invoker
  gcloud run services add-iam-policy-binding content-api --region="${REGION}" --project="${PROJECT}" \
    --member="serviceAccount:$(sa web)" --role=roles/run.invoker
  if [[ -n "${WEB_ORIGIN:-}" ]]; then
    tmp="$(mktemp)"
    sed "s|https://REPLACE_WITH_WEB_ORIGIN|${WEB_ORIGIN}|" deploy/gcs-cors.json > "${tmp}"
    gcloud storage buckets update "gs://${BUCKET_PREFIX}-staging" --cors-file="${tmp}"
    rm -f "${tmp}"
  else
    echo "Set WEB_ORIGIN to apply deploy/gcs-cors.json on the staging bucket (browser signed uploads)."
  fi
  echo "Enable IAP on the web Cloud Run service, then lock document-api/content-api to authenticated invokers only."
}

create_alerts() {
  if [[ "${ALERTS:-0}" != "1" ]]; then
    return
  fi
  echo "== Alerts (add notification channels to the policies afterwards)"
  gcloud logging metrics create yesbook_outbox_oldest_age --project="${PROJECT}" --config-from-file=deploy/monitoring/outbox-age-metric.yaml
  gcloud logging metrics create yesbook_stuck_jobs --project="${PROJECT}" --config-from-file=deploy/monitoring/stuck-jobs-metric.yaml
  gcloud monitoring policies create --project="${PROJECT}" --policy-from-file=deploy/monitoring/outbox-age-alert.json
  gcloud monitoring policies create --project="${PROJECT}" --policy-from-file=deploy/monitoring/stuck-jobs-alert.json
}

require_variables
enable_apis
create_database_vm
create_service_accounts
create_secrets
create_buckets
create_pubsub
grant_project_roles
configure_worker_env
build_images
apply_schema
deploy_worker
deploy_apis
deploy_publisher
deploy_web
create_alerts
echo "Done."
