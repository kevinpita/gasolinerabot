# gasolinerabot

Deployment scaffold for a Telegram bot. Application code will be added later.

Uses the same deployment pattern as [reelrelay](https://github.com/kevinpita/reelrelay):

```text
GitHub Actions -> GHCR image -> image reference in Git -> Argo CD -> Kubernetes
OpenBao -> External Secrets Operator -> Kubernetes Secret -> TELEGRAM_BOT_TOKEN
```

## Prepared

- Helm chart at `infra/chart`, for namespace `gasolinerabot`.
- One replica with `Recreate`, suitable for Telegram long polling.
- Linux amd64, UID/GID 10001, read-only root filesystem, writable `/tmp`.
- Separate OpenBao read policy and auth role at `infra/openbao`.
- Secret `gasolinerabot-secrets`, with key `TELEGRAM_BOT_TOKEN`.
- GHCR target `ghcr.io/kevinpita/gasolinerabot`, with eight-character commit tags.
- CI checks for Helm rendering and Terraform validation.

No application, Dockerfile, bot token, or working image is included. Deployment and OpenBao sync are disabled by default. Image builds are disabled until the GitHub repository variable `BUILD_IMAGE` is `true`. No live infrastructure is changed by CI.

## Add the code

1. Add the existing bot code and its dependencies. Read `TELEGRAM_BOT_TOKEN` from the environment. Confirm that it uses long polling before keeping the one-replica deployment model.
2. Add a Dockerfile and a restrictive `.dockerignore` for the actual runtime. Support Linux amd64, UID/GID 10001, a read-only root filesystem, writable `/tmp`, and SIGTERM shutdown. Add explicit storage if the bot needs persistent data.
3. Add app tests to CI and make the image job depend on them. Add `scripts/smoke-image.sh`, which receives the built image name and must test it without live credentials. The image job requires this script before publication.
4. If the app serves `/healthz` and `/readyz` on port 8080, set `health.enabled: true`. Otherwise adapt the probes before production use.
5. Enable image builds with `gh variable set BUILD_IMAGE --body true`. Main-branch pushes and `v*.*.*` tags publish images. Pull requests build and test but do not publish. CI uses `GITHUB_TOKEN`, not the Telegram token.

## Add to your deployment

Configure your Argo CD Application with:

```yaml
source:
  repoURL: https://github.com/kevinpita/gasolinerabot.git
  targetRevision: main
  path: infra/chart
destination:
  server: https://kubernetes.default.svc
  namespace: gasolinerabot
syncPolicy:
  syncOptions:
    - CreateNamespace=true
```

This is an Application spec fragment, not a complete manifest. Add it through your existing deployment repo. Do not manage the same installation with both Helm and Argo CD.

Follow [OpenBao setup](infra/openbao/README.md) to provision the separate role and store the token at `kv/apps/gasolinerabot`. Without ESO, provide the same Kubernetes Secret through your existing secret-management process.

After an image is published, set `image.tag` or `image.digest` in `infra/chart/values.yaml`. A digest takes priority. Set `deployment.enabled: true` only when the image and secret are ready. For private GHCR images, supply `imagePullSecrets`, or make the package public. Repository visibility does not set package visibility.

Registry publication alone does not deploy a new version. Commit the new image reference and sync Argo CD. No automatic image updater is configured.

## Local checks

Requires Helm, Terraform, and Bash:

```bash
bash scripts/check-infra.sh
terraform -chdir=infra/openbao fmt -check
terraform -chdir=infra/openbao init -backend=false -lockfile=readonly
terraform -chdir=infra/openbao validate
```

Keep credentials in OpenBao or private local `.env` files, never in Git.
