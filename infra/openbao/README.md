# OpenBao access

This uses the same shared OpenBao and External Secrets Operator (ESO) setup as Reelrelay. Do not create or change the shared Kubernetes auth mount, KV engine, or ESO installation here.

Application resources:

- KV v2 entry: `kv/apps/gasolinerabot`
- Required entry keys: `TELEGRAM_BOT_TOKEN`, `POSTGRES_PASSWORD`, `POSTGRES_APP_PASSWORD`
- Terraform policy: `gasolinerabot-read`
- Kubernetes auth role and namespace: `gasolinerabot`
- ESO ServiceAccount: `gasolinerabot:openbao-reader`
- Kubernetes Secret: `gasolinerabot-secrets`

Terraform manages only the policy and role. It does not manage or read application credentials. The Helm chart manages the ESO resources and TokenReview-only RBAC. The bot pod does not receive a Kubernetes API token.

## Setup

1. Confirm the shared `kubernetes/` auth mount uses the login JWT for TokenReview. ESO must be allowed to request tokens for `openbao-reader`.
2. Set `openbao.audience` in `infra/chart/values.yaml` to an audience accepted by your Kubernetes API. Terraform reads this file too. Do not override the audience only in Argo CD.
3. Use your existing administrator account to log in. Run from the repository root:

```bash
export BAO_ADDR=https://bao.kevinpita.com
umask 077
state_dir="$HOME/.local/state/gasolinerabot/openbao"
mkdir -p "$state_dir"
chmod 700 "$state_dir"
bao login -method=userpass -no-print username=YOUR_ADMIN_USER
export VAULT_TOKEN="$(bao print token)"
terraform -chdir=infra/openbao init \
  -backend-config="path=$state_dir/terraform.tfstate"
terraform -chdir=infra/openbao validate
terraform -chdir=infra/openbao plan -out="$state_dir/access.tfplan"
```

Review the plan. A new setup must add only the application policy and role. If either exists, check ownership and import it before you plan. Then apply:

```bash
terraform -chdir=infra/openbao apply "$state_dir/access.tfplan"
unset VAULT_TOKEN
rm "$state_dir/access.tfplan"
```

Keep the state private and backed up. Use the same state path for later runs.

## Store application credentials

Use the OpenBao UI to add the Telegram token and two distinct, randomly generated database passwords. `POSTGRES_PASSWORD` is for the database administrator. `POSTGRES_APP_PASSWORD` is for the bot's non-superuser role.

For the **first setup only**, this Bash command prompts for the Telegram token and generates both database passwords. It streams the JSON directly to OpenBao. Do not use it to rotate an initialized database, because it replaces the entry and PostgreSQL keeps its existing role passwords.

```bash
set -o pipefail
python3 -c 'import getpass,json,secrets; print(json.dumps({"TELEGRAM_BOT_TOKEN":getpass.getpass("Telegram bot token: "),"POSTGRES_PASSWORD":secrets.token_urlsafe(32),"POSTGRES_APP_PASSWORD":secrets.token_urlsafe(32)}))' |
  bao kv put -mount=kv apps/gasolinerabot -
```

Do not put credentials in Git, Terraform variables, or GitHub Actions secrets. The bot receives only its token and application password. The PostgreSQL pod receives the two database passwords.

If you disable the bundled database and use an external database instead, add `DATABASE_URL` for the bot. See [database operations](../../docs/database.md) for backup and password rotation.

## Sync

Set `openbao.enabled: true` in the chart values. Argo CD must use namespace `gasolinerabot`, with `CreateNamespace=true`, and allow the chart's ClusterRole and ClusterRoleBinding. Keep one chart installation in this namespace. Check existing resource ownership before the first sync.

The default `deployment.enabled: false` lets you prepare the secret, database, and preferences import before starting the bot. After sync, check without printing secret data:

```bash
kubectl -n gasolinerabot wait secretstore/openbao --for=condition=Ready --timeout=120s
kubectl -n gasolinerabot wait externalsecret/gasolinerabot-secrets --for=condition=Ready --timeout=120s
```

ESO refreshes each minute. After a token rotation, restart the Deployment to load the new environment value. Removing the ExternalSecret can remove its owned Kubernetes Secret. Removing the policy or role does not erase the KV entry or revoke the Telegram token.
