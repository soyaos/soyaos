# SoyaOS ciphertext relay deployment

> [!WARNING]
> **Active development; not formally released.** This deployment is alpha,
> functionality is unstable, and breaking changes may happen at any time.
> Do not treat it as production-ready infrastructure.

This directory runs the APP-510 relay. It exposes UDP `7443` for opaque QUIC
datagrams and a loopback-only HTTP health endpoint on `7480`.

## Security model

- Moon ↔ Comet mTLS terminates only on those two nodes.
- The relay secret signs short-lived routing tokens; it is not a TLS key.
- Never commit `.env`, a token, certificate, private key, or server IP list
  containing private infrastructure details.
- Read and preserve [`docs/security/relay-privacy.md`](../../docs/security/relay-privacy.md).

## Local smoke test

```bash
cd /path/to/soyaos
cp deploy/relay/.env.example deploy/relay/.env

# Generate locally. This writes only to the ignored .env file.
secret="$(openssl rand -base64 32)"
sed -i.bak "s|replace-with-openssl-rand-base64-32|${secret}|" deploy/relay/.env
rm deploy/relay/.env.bak

docker compose -f deploy/relay/docker-compose.yml up --build -d
curl --fail http://127.0.0.1:7480/healthz
docker compose -f deploy/relay/docker-compose.yml down
```

## US-West alpha node

Recommended minimum while traffic is tiny: a US-West VM with 1 shared vCPU,
512 MiB RAM, 10 GiB disk, and at least 500 GiB outbound transfer. The current
[DigitalOcean reference price](https://www.digitalocean.com/pricing/droplets)
is `$4/month`; bandwidth above the included pool is billed separately.

The APP-510 alpha node is live in SFO3 at `137.184.35.52:7443/udp`. Cloud
configuration uses this stable-for-the-life-of-the-Droplet IP until
`relay-us-west.soyaos.ai` can be created with a DNS-write credential.

Server preparation after the VM exists:

```bash
# Run on the Ubuntu server as root.
apt-get update
apt-get install -y ca-certificates docker.io docker-compose-v2 ufw
ufw default deny incoming
ufw allow 22/tcp
ufw allow 7443/udp
ufw --force enable

mkdir -p /opt/soyaos-relay
cd /opt/soyaos-relay
# Copy this checkout or a pinned source archive here. Do not use an unpinned
# floating image while the project is pre-release.
```

Create `/opt/soyaos-relay/deploy/relay/.env` on the server with mode `0600`:

```dotenv
SOYAOS_RELAY_SECRET=<paste output of: openssl rand -base64 32>
```

Then start and verify:

```bash
docker compose -f deploy/relay/docker-compose.yml up --build -d
docker compose -f deploy/relay/docker-compose.yml ps
curl --fail http://127.0.0.1:7480/healthz
ss -lunp | grep ':7443'
```

The health port is published only on server loopback. Monitor it through an
SSH check; do not open `7480/tcp` to the Internet.

## Mint a five-minute test route

Run inside the relay container so the signing secret never leaves the server:

```bash
docker compose -f deploy/relay/docker-compose.yml exec relay \
  /soyaos relay token \
  --endpoint 137.184.35.52:7443 \
  --ttl 5m
```

The command prints separate Moon and Comet URIs. They contain a bearer token;
do not paste them into logs or commit them. Cloud configuration stores only:

```dotenv
SOYAOS_RELAY_ENDPOINTS=relay+udp://137.184.35.52:7443
SOYAOS_RELAY_FREE_RATE_MBPS=10
```

Planet adds the token and side at scheduling time.

## Rollback

```bash
docker compose -f deploy/relay/docker-compose.yml down
```

Deleting the VM is the only way to stop VM billing. Preserve no relay payload:
there should be none on disk. Retain only the deployment commit SHA and
aggregate health counters in APP-510.
