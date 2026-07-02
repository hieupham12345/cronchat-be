#!/usr/bin/env bash
# =============================================================================
# CronChat — AWS provisioning (EC2 single VM + RDS MySQL) in ap-southeast-1.
#
# ⚠️  This CREATES BILLABLE RESOURCES. Review every variable below first.
#     Recommended: run it section by section (copy/paste) the first time
#     rather than all at once, so you can eyeball each resource.
#
# Prereqs: aws CLI v2 configured (`aws sts get-caller-identity` works).
# Teardown: see the "TEARDOWN" section at the bottom of deploy/README.md.
# =============================================================================
set -euo pipefail

# ---------------------------- CONFIG (edit me) -------------------------------
REGION="ap-southeast-1"
PROJECT="cronchat"
KEY_NAME="${PROJECT}-key"
INSTANCE_TYPE="t3.micro"          # FREE TIER (750h/mo). 1 vCPU / 1 GiB.
                                  # Bump to t3.small (~$15/mo) if the box feels tight.
VOLUME_GB="20"                    # root EBS gp3 (free tier: up to 30 GB)
DB_INSTANCE_CLASS="db.t4g.micro"  # FREE TIER RDS (750h/mo Single-AZ)
DB_ALLOC_GB="20"
DB_NAME="cronchat"
DB_USER="cronchat_admin"
# DB password: export DB_PASSWORD=... before running, or you'll be prompted.
DB_PASSWORD="${DB_PASSWORD:-}"
# ----------------------------------------------------------------------------

export AWS_DEFAULT_REGION="$REGION"

echo "Account: $(aws sts get-caller-identity --query Account --output text)  Region: $REGION"
read -r -p "This will create billable AWS resources. Continue? (yes/no) " ans
[[ "$ans" == "yes" ]] || { echo "Aborted."; exit 1; }

if [[ -z "$DB_PASSWORD" ]]; then
	read -r -s -p "Enter a strong RDS master password: " DB_PASSWORD; echo
fi

# ---- Networking: use the default VPC + its subnets --------------------------
VPC_ID=$(aws ec2 describe-vpcs --filters Name=isDefault,Values=true \
	--query 'Vpcs[0].VpcId' --output text)
echo "Default VPC: $VPC_ID"

mapfile -t SUBNETS < <(aws ec2 describe-subnets --filters Name=vpc-id,Values="$VPC_ID" \
	--query 'Subnets[].SubnetId' --output text | tr '\t' '\n')
echo "Subnets: ${SUBNETS[*]}"

MY_IP=$(curl -fsSL https://checkip.amazonaws.com)
echo "Your public IP (for SSH allow-list): $MY_IP"

# ---- Security groups --------------------------------------------------------
EC2_SG=$(aws ec2 create-security-group --group-name "${PROJECT}-ec2-sg" \
	--description "CronChat EC2" --vpc-id "$VPC_ID" --query GroupId --output text)
RDS_SG=$(aws ec2 create-security-group --group-name "${PROJECT}-rds-sg" \
	--description "CronChat RDS" --vpc-id "$VPC_ID" --query GroupId --output text)
echo "EC2 SG: $EC2_SG   RDS SG: $RDS_SG"

# SSH only from your IP; HTTP/HTTPS from anywhere (for Caddy TLS).
aws ec2 authorize-security-group-ingress --group-id "$EC2_SG" \
	--ip-permissions "IpProtocol=tcp,FromPort=22,ToPort=22,IpRanges=[{CidrIp=${MY_IP}/32}]"
aws ec2 authorize-security-group-ingress --group-id "$EC2_SG" \
	--ip-permissions "IpProtocol=tcp,FromPort=80,ToPort=80,IpRanges=[{CidrIp=0.0.0.0/0}]"
aws ec2 authorize-security-group-ingress --group-id "$EC2_SG" \
	--ip-permissions "IpProtocol=tcp,FromPort=443,ToPort=443,IpRanges=[{CidrIp=0.0.0.0/0}]"
# For a quick HTTP-only test WITHOUT a domain/Caddy, also open 5555 (then set
# the compose port to "5555:5555"). Comment out once behind TLS:
# aws ec2 authorize-security-group-ingress --group-id "$EC2_SG" \
#   --ip-permissions "IpProtocol=tcp,FromPort=5555,ToPort=5555,IpRanges=[{CidrIp=0.0.0.0/0}]"

# RDS reachable ONLY from the EC2 SG (never public).
aws ec2 authorize-security-group-ingress --group-id "$RDS_SG" \
	--ip-permissions "IpProtocol=tcp,FromPort=3306,ToPort=3306,UserIdGroupPairs=[{GroupId=${EC2_SG}}]"

# ---- RDS MySQL --------------------------------------------------------------
aws rds create-db-subnet-group --db-subnet-group-name "${PROJECT}-subnets" \
	--db-subnet-group-description "CronChat" --subnet-ids "${SUBNETS[@]}"

aws rds create-db-instance \
	--db-instance-identifier "${PROJECT}-db" \
	--db-instance-class "$DB_INSTANCE_CLASS" \
	--engine mysql \
	--allocated-storage "$DB_ALLOC_GB" --storage-type gp3 \
	--master-username "$DB_USER" --master-user-password "$DB_PASSWORD" \
	--db-name "$DB_NAME" \
	--vpc-security-group-ids "$RDS_SG" \
	--db-subnet-group-name "${PROJECT}-subnets" \
	--no-publicly-accessible \
	--backup-retention-period 7 \
	--no-multi-az

echo "Waiting for RDS to become available (this takes several minutes)..."
aws rds wait db-instance-available --db-instance-identifier "${PROJECT}-db"
RDS_ENDPOINT=$(aws rds describe-db-instances --db-instance-identifier "${PROJECT}-db" \
	--query 'DBInstances[0].Endpoint.Address' --output text)
echo "RDS endpoint: $RDS_ENDPOINT"

# ---- Key pair ---------------------------------------------------------------
if [[ ! -f "${KEY_NAME}.pem" ]]; then
	aws ec2 create-key-pair --key-name "$KEY_NAME" \
		--query KeyMaterial --output text > "${KEY_NAME}.pem"
	chmod 400 "${KEY_NAME}.pem"
	echo "Saved SSH key: ${KEY_NAME}.pem"
fi

# ---- EC2 instance -----------------------------------------------------------
AMI_ID=$(aws ssm get-parameter \
	--name /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64 \
	--query 'Parameter.Value' --output text)
echo "AMI: $AMI_ID"

INSTANCE_ID=$(aws ec2 run-instances \
	--image-id "$AMI_ID" --instance-type "$INSTANCE_TYPE" \
	--key-name "$KEY_NAME" --security-group-ids "$EC2_SG" \
	--subnet-id "${SUBNETS[0]}" --associate-public-ip-address \
	--block-device-mappings "DeviceName=/dev/xvda,Ebs={VolumeSize=${VOLUME_GB},VolumeType=gp3}" \
	--user-data "file://$(dirname "$0")/user-data.sh" \
	--tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=${PROJECT}}]" \
	--query 'Instances[0].InstanceId' --output text)
echo "Launched EC2: $INSTANCE_ID — waiting for running state..."
aws ec2 wait instance-running --instance-ids "$INSTANCE_ID"

# ---- Elastic IP -------------------------------------------------------------
ALLOC_ID=$(aws ec2 allocate-address --domain vpc --query AllocationId --output text)
aws ec2 associate-address --instance-id "$INSTANCE_ID" --allocation-id "$ALLOC_ID" >/dev/null
EIP=$(aws ec2 describe-addresses --allocation-ids "$ALLOC_ID" \
	--query 'Addresses[0].PublicIp' --output text)

cat <<EOF

============================================================
✅ Provisioned. Next steps (see deploy/README.md):
   Elastic IP : $EIP
   Instance   : $INSTANCE_ID
   RDS host   : $RDS_ENDPOINT
   SSH        : ssh -i ${KEY_NAME}.pem ec2-user@$EIP

  1) Import schema:  mysql -h $RDS_ENDPOINT -u $DB_USER -p $DB_NAME < database.sql
  2) Copy code to /opt/cronchat, create deploy/.env.prod (MYSQL_HOST=$RDS_ENDPOINT)
  3) docker compose -f deploy/docker-compose.prod.yml up -d --build
============================================================
EOF
