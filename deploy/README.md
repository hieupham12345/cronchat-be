# CronChat — AWS Deployment (EC2 single VM + RDS MySQL)

Region: **ap-southeast-1 (Singapore)**. Nothing here runs automatically — review, then execute.

## Architecture

```
                 Internet
                    │  (HTTPS/WSS 443, or HTTP 5555 for testing)
             ┌──────▼───────┐        EC2 (t3.small, Amazon Linux 2023)
             │ Elastic IP    │        ┌───────────────────────────────┐
             │  ───────────► │        │ docker compose:               │
             └───────────────┘        │   • api  (Go server :5555)    │
                                      │   • caddy (optional TLS)      │
                                      │ EBS gp3 20GB:                 │
                                      │   /opt/cronchat/data/* uploads│
                                      └──────────────┬────────────────┘
                                                     │ 3306 (private SG only)
                                            ┌────────▼─────────┐
                                            │ RDS MySQL         │
                                            │ db.t4g.micro      │
                                            └───────────────────┘
```

- **Uploads persist** on the host EBS volume (`data/*`) — no code change needed.
- **Single instance only**: the WebSocket hub is in-memory. Do not put this behind
  an autoscaling group / multiple tasks until a pub/sub layer (e.g. Redis) is added.
- **RDS is private** — reachable only from the EC2 security group.

## Cost — Free Tier vs after (ap-southeast-1)

Configured for the **12-month AWS Free Tier** (new accounts):

| Resource | Spec | Free tier (first 12 mo) | After free tier |
|---|---|---|---|
| EC2 | **t3.micro** | 750 h/mo → **$0** | ~$8.5/mo |
| EBS | 20 GB gp3 | ≤30 GB → **$0** | ~$1.6/mo |
| RDS | **db.t4g.micro** + 20 GB | 750 h/mo Single-AZ → **$0** | ~$13/mo |
| Elastic IP | attached to running instance | **$0** | $0 |
| **Total** | | **~$0/mo** | **~$23/mo** |

Notes:
- Free tier is **12 months from account creation** and requires exactly these
  sizes. One instance running 24/7 = ~730 h < the 750 h/mo allowance.
- `t3.micro` has only **1 GiB RAM** — `user-data.sh` adds a 2 GiB swap file so the
  Docker build and app don't get OOM-killed. If it feels tight later, bump
  `INSTANCE_TYPE` to `t3.small` (leaves free tier, ~$15/mo).
- Watch **data-transfer-out** and RDS backup storage if traffic grows — those can
  exceed the free allowance.

## Files

| File | Purpose |
|---|---|
| `provision.sh` | Creates key pair, security groups, RDS, EC2, Elastic IP. |
| `user-data.sh` | EC2 first-boot: installs Docker + Compose v2, preps `/opt/cronchat`. |
| `docker-compose.prod.yml` | Runs the app (build from `Dockerfile`), mounts uploads. |
| `Dockerfile` | Static Go build; **no secrets baked in**. |
| `.env.prod.example` | Template for runtime env (copy to `.env.prod`, git-ignored). |
| `Caddyfile` | Optional TLS reverse proxy (needs a domain). |

## Steps

### 0. Prereqs
```bash
aws sts get-caller-identity          # must succeed
export AWS_DEFAULT_REGION=ap-southeast-1
```

### 1. Provision infrastructure
```bash
cd deploy
export DB_PASSWORD='<a-strong-password>'
bash provision.sh                    # prompts for confirmation; ~5-10 min (RDS)
```
Note the printed **Elastic IP** and **RDS endpoint**.

### 2. Import the schema into RDS
`database.sql` is authoritative but note: the stored procedure was reconstructed
from code — sanity-check it before relying on it in prod.
```bash
mysql -h <RDS_ENDPOINT> -u cronchat_admin -p cronchat < ../database.sql
```
(Run from a machine allowed to reach RDS — e.g. temporarily open 3306 to your IP,
or run it from the EC2 box after step 3.)

### 3. Get the code onto EC2
```bash
ssh -i cronchat-key.pem ec2-user@<ELASTIC_IP> 'ls /opt/cronchat'   # confirm ready
# then, from repo root:
rsync -az --exclude data --exclude '*.pem' -e "ssh -i deploy/cronchat-key.pem" \
  ./ ec2-user@<ELASTIC_IP>:/opt/cronchat/
```
(Or `git clone` your remote onto the box if you have one.)

### 4. Create the runtime env file on the host
```bash
ssh -i deploy/cronchat-key.pem ec2-user@<ELASTIC_IP>
cd /opt/cronchat
cp deploy/.env.prod.example deploy/.env.prod
nano deploy/.env.prod        # set GO_SECRET_KEY, MYSQL_HOST=<RDS_ENDPOINT>, MYSQL_PASSWORD
```

### 5. Build & run
Compose resolves relative paths from the compose file's dir, so pass
`--project-directory .` to keep `env_file` / build context / volumes anchored at
the repo root:
```bash
cd /opt/cronchat
# HTTP-only test (no domain): publish 5555 publicly
sed -i 's/127.0.0.1:5555:5555/5555:5555/' deploy/docker-compose.prod.yml
docker compose -f deploy/docker-compose.prod.yml --project-directory . up -d --build
docker compose -f deploy/docker-compose.prod.yml --project-directory . logs -f api
```
Health check (HTTP test mode): from the box, `curl -i localhost:5555/rooms` should
return `401` (auth required) — that means the server is up.

### 6. (Recommended) TLS + domain for browser WebSocket
Browsers block `ws://` from an `https://` frontend (mixed content), so a real
frontend needs **`wss://`** → the backend needs TLS.
1. Point a domain's A record at the Elastic IP.
2. Edit `Caddyfile` (set your domain), uncomment the `caddy` service in
   `docker-compose.prod.yml`, set `api` ports to `127.0.0.1:5555:5555`.
3. `docker compose -f deploy/docker-compose.prod.yml up -d`.

## ⚠️ Production security follow-ups (in the app code)
These are currently dev-friendly and should change for a public HTTPS deployment:
- **Refresh cookie** (`auth.go`) is set with `Secure:false` and `SameSite=Lax`.
  For HTTPS — and especially if the frontend is on a **different domain** — set
  `Secure:true` and `SameSite=None`, otherwise the cookie-based `/ws` auth and
  `/auth/refresh` will fail cross-site.
- **CORS** currently echoes any Origin with credentials (`middleware.go`). Lock it
  to your frontend origin(s) before going public.
- Consider moving `GO_SECRET_KEY` / DB password to **SSM Parameter Store** or
  **Secrets Manager** instead of a plaintext `.env.prod` on disk.

## TEARDOWN (stop all billing)
```bash
export AWS_DEFAULT_REGION=ap-southeast-1
aws ec2 terminate-instances --instance-ids <INSTANCE_ID>
aws ec2 release-address --allocation-id <ALLOC_ID>
aws rds delete-db-instance --db-instance-identifier cronchat-db --skip-final-snapshot
aws rds delete-db-subnet-group --db-subnet-group-name cronchat-subnets   # after RDS gone
aws ec2 delete-security-group --group-id <EC2_SG>   # after instance gone
aws ec2 delete-security-group --group-id <RDS_SG>   # after RDS gone
aws ec2 delete-key-pair --key-name cronchat-key
```
