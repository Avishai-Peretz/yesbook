#!/usr/bin/env bash
# One-time setup of Yesbook in an empty Google Cloud project, matching the
# container view: Document API, Content API, outbox publisher (Cloud Run),
# processing worker (Cloud Run Job) driven by Cloud Workflows, Cloud SQL for
# PostgreSQL on a private IP, four GCS buckets, a Pub/Sub topic, and alerts.
#
# Usage: PROJECT=my-project REGION=europe-west1 DB_PASSWORD=... \
#        API_KEYS="tenant-a:<16+ char key>" ./deploy/setup.sh
# Run it from the yesbook directory. Each service gets its own service
# account with only the roles it needs.
set -euo pipefail

: "${PROJECT:?set PROJECT}" "${REGION:?set REGION}" "${DB_PASSWORD:?set DB_PASSWORD}" "${API_KEYS:?set API_KEYS}"
TAG="${TAG:-v1}"
NETWORK="${NETWORK:-default}"
SUBNET="${SUBNET:-default}"
GEMINI_LOCATION="${GEMINI_LOCATION:-global}"
REPO="${REGION}-docker.pkg.dev/${PROJECT}/yesbook"
WORKFLOW="yesbook-processing"
TOPIC="yesbook-document-events"
BUCKET_PREFIX="${BUCKET_PREFIX:-${PROJECT}-yesbook}"

gcloud config set project "${PROJECT}"

echo "== APIs"
gcloud services enable run.googleapis.com workflows.googleapis.com workflowexecutions.googleapis.com \
  sqladmin.googleapis.com servicenetworking.googleapis.com pubsub.googleapis.com storage.googleapis.com \
  secretmanager.googleapis.com artifactregistry.googleapis.com cloudbuild.googleapis.com \
  cloudscheduler.googleapis.com aiplatform.googleapis.com documentai.googleapis.com \
  logging.googleapis.com monitoring.googleapis.com iamcredentials.googleapis.com

echo "== Service accounts"
for sa in document-api content-api outbox-publisher worker workflow scheduler; do
  gcloud iam service-accounts create "yb-${sa}" --display-name="Yesbook ${sa}"
done
sa() { echo "yb-$1@${PROJECT}.iam.gserviceaccount.com"; }

echo "== Cloud SQL (private IP only)"
gcloud compute addresses create google-managed-services-"${NETWORK}" --global --purpose=VPC_PEERING \
  --prefix-length=16 --network="${NETWORK}"
gcloud services vpc-peerings connect --service=servicenetworking.googleapis.com \
  --ranges=google-managed-services-"${NETWORK}" --network="${NETWORK}"
gcloud sql instances create yesbook --database-version=POSTGRES_17 --edition=ENTERPRISE \
  --tier=db-custom-1-3840 --region="${REGION}" --network="projects/${PROJECT}/global/networks/${NETWORK}" \
  --no-assign-ip --availability-type=zonal
gcloud sql databases create yesbook --instance=yesbook
gcloud sql users create yesbook --instance=yesbook --password="${DB_PASSWORD}"
DB_IP="$(gcloud sql instances describe yesbook --format='value(ipAddresses[0].ipAddress)')"

echo "== Secrets"
printf '%s' "postgres://yesbook:${DB_PASSWORD}@${DB_IP}:5432/yesbook?sslmode=require" |
  gcloud secrets create yesbook-database-url --replication-policy=automatic --data-file=-
printf '%s' "${API_KEYS}" | gcloud secrets create yesbook-api-keys --replication-policy=automatic --data-file=-
for s in document-api content-api outbox-publisher worker; do
  gcloud secrets add-iam-policy-binding yesbook-database-url --member="serviceAccount:$(sa "$s")" \
    --role=roles/secretmanager.secretAccessor
done
for s in document-api content-api; do
  gcloud secrets add-iam-policy-binding yesbook-api-keys --member="serviceAccount:$(sa "$s")" \
    --role=roles/secretmanager.secretAccessor
done

echo "== Buckets"
for b in staging originals evidence derived; do
  gcloud storage buckets create "gs://${BUCKET_PREFIX}-${b}" --location="${REGION}" \
    --uniform-bucket-level-access --public-access-prevention
done
gcloud storage buckets update "gs://${BUCKET_PREFIX}-staging" --lifecycle-file=deploy/staging-lifecycle.json
bind_bucket() {
  gcloud storage buckets add-iam-policy-binding "gs://${BUCKET_PREFIX}-$1" \
    --member="serviceAccount:$(sa "$2")" --role="$3"
}
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
  gcloud iam service-accounts add-iam-policy-binding "$(sa "$s")" \
    --member="serviceAccount:$(sa "$s")" --role=roles/iam.serviceAccountTokenCreator
done

echo "== Pub/Sub"
gcloud pubsub topics create "${TOPIC}"
gcloud pubsub topics add-iam-policy-binding "${TOPIC}" \
  --member="serviceAccount:$(sa outbox-publisher)" --role=roles/pubsub.publisher

echo "== Project roles"
bind_project() {
  gcloud projects add-iam-policy-binding "${PROJECT}" --member="serviceAccount:$(sa "$1")" \
    --role="$2" --condition=None >/dev/null
}
bind_project document-api roles/workflows.invoker
bind_project worker roles/aiplatform.user
bind_project worker roles/documentai.apiUser
bind_project workflow roles/run.viewer
bind_project workflow roles/logging.logWriter
gcloud iam service-accounts add-iam-policy-binding "$(sa worker)" \
  --member="serviceAccount:$(sa workflow)" --role=roles/iam.serviceAccountUser

echo "== Images"
gcloud artifacts repositories create yesbook --repository-format=docker --location="${REGION}"
gcloud builds submit --config=deploy/cloudbuild.yaml --substitutions="_REPO=${REPO},_TAG=${TAG}" .

VPC=(--network="${NETWORK}" --subnet="${SUBNET}" --vpc-egress=private-ranges-only)
DB_SECRET="YESBOOK_DATABASE_URL=yesbook-database-url:latest"
BUCKETS="YESBOOK_BUCKET_STAGING=${BUCKET_PREFIX}-staging,YESBOOK_BUCKET_ORIGINALS=${BUCKET_PREFIX}-originals,YESBOOK_BUCKET_EVIDENCE=${BUCKET_PREFIX}-evidence,YESBOOK_BUCKET_DERIVED=${BUCKET_PREFIX}-derived"

echo "== Schema (review migrations/ before this step)"
gcloud run jobs deploy yesbook-migrate --image="${REPO}/migrate:${TAG}" --region="${REGION}" \
  --service-account="$(sa worker)" "${VPC[@]}" --set-secrets="${DB_SECRET}" --max-retries=0
gcloud run jobs execute yesbook-migrate --region="${REGION}" --wait

echo "== Processing worker and workflow"
gcloud run jobs deploy yesbook-worker --image="${REPO}/worker:${TAG}" --region="${REGION}" \
  --service-account="$(sa worker)" "${VPC[@]}" --tasks=1 --max-retries=0 --task-timeout=30m \
  --cpu=2 --memory=2Gi --set-secrets="${DB_SECRET}" \
  --set-env-vars="${BUCKETS},YESBOOK_PROJECT=${PROJECT},YESBOOK_GEMINI_LOCATION=${GEMINI_LOCATION}"
gcloud run jobs add-iam-policy-binding yesbook-worker --region="${REGION}" \
  --member="serviceAccount:$(sa workflow)" --role=roles/run.jobsExecutorWithOverrides
gcloud workflows deploy "${WORKFLOW}" --location="${REGION}" --source=deploy/workflow.yaml \
  --service-account="$(sa workflow)"

echo "== APIs"
gcloud run deploy document-api --image="${REPO}/document-api:${TAG}" --region="${REGION}" \
  --service-account="$(sa document-api)" "${VPC[@]}" --allow-unauthenticated \
  --set-secrets="${DB_SECRET},YESBOOK_API_KEYS=yesbook-api-keys:latest" \
  --set-env-vars="${BUCKETS},YESBOOK_WORKFLOW=projects/${PROJECT}/locations/${REGION}/workflows/${WORKFLOW}"
gcloud run deploy content-api --image="${REPO}/content-api:${TAG}" --region="${REGION}" \
  --service-account="$(sa content-api)" "${VPC[@]}" --allow-unauthenticated \
  --set-secrets="${DB_SECRET},YESBOOK_API_KEYS=yesbook-api-keys:latest" --set-env-vars="${BUCKETS}"

echo "== Outbox publisher (always on, private)"
gcloud run deploy outbox-publisher --image="${REPO}/outbox-publisher:${TAG}" --region="${REGION}" \
  --service-account="$(sa outbox-publisher)" "${VPC[@]}" --no-allow-unauthenticated \
  --min-instances=1 --max-instances=1 --no-cpu-throttling --set-secrets="${DB_SECRET}" \
  --set-env-vars="YESBOOK_PROJECT=${PROJECT},YESBOOK_TOPIC=${TOPIC}"
gcloud run services add-iam-policy-binding outbox-publisher --region="${REGION}" \
  --member="serviceAccount:$(sa scheduler)" --role=roles/run.invoker
PUBLISHER_URL="$(gcloud run services describe outbox-publisher --region="${REGION}" --format='value(status.url)')"
gcloud scheduler jobs create http yesbook-outbox-sweep --location="${REGION}" --schedule="* * * * *" \
  --uri="${PUBLISHER_URL}/sweep" --http-method=POST \
  --oidc-service-account-email="$(sa scheduler)" --oidc-token-audience="${PUBLISHER_URL}"

echo "== Alerts (add notification channels to the policies afterwards)"
gcloud logging metrics create yesbook_outbox_oldest_age --config-from-file=deploy/monitoring/outbox-age-metric.yaml
gcloud logging metrics create yesbook_stuck_jobs --config-from-file=deploy/monitoring/stuck-jobs-metric.yaml
gcloud monitoring policies create --policy-from-file=deploy/monitoring/outbox-age-alert.json
gcloud monitoring policies create --policy-from-file=deploy/monitoring/stuck-jobs-alert.json

echo "Done. Optional extractors: set YESBOOK_DOCUMENTAI_*, YESBOOK_DOCLING_* or YESBOOK_OPENAI_* on yesbook-worker."
