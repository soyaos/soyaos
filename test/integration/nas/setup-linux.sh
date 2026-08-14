#!/usr/bin/env bash
set -euo pipefail

# Provision an ephemeral Ubuntu VM with four private NAS protocol services.
# All credentials are generated inside the VM and written to a mode-0600 env
# file. Nothing secret is printed or committed.

test_home="${HOME}"
test_root="/srv/soyaos-nas-e2e"
env_dir="${test_home}/.config/soyaos"
env_file="${env_dir}/nas-e2e.env"
minio_release="RELEASE.2025-04-22T22-12-26Z"
minio_base="https://dl.min.io/server/minio/release/linux-arm64/archive"

password="$(openssl rand -hex 24)"
access_key="soyaos$(openssl rand -hex 8)"
secret_key="$(openssl rand -hex 32)"

sudo apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
  apache2 apache2-utils ca-certificates curl nfs-kernel-server openssl samba

sudo install -d -m 0777 \
  "${test_root}/smb" "${test_root}/nfs" "${test_root}/webdav" "${test_root}/minio"

if ! id soyaos-test >/dev/null 2>&1; then
  sudo useradd --no-create-home --shell /usr/sbin/nologin soyaos-test
fi
printf '%s\n%s\n' "${password}" "${password}" | sudo smbpasswd -s -a soyaos-test >/dev/null
sudo tee /etc/samba/soyaos-e2e.conf >/dev/null <<'EOF'
[soyaos-test]
  path = /srv/soyaos-nas-e2e/smb
  browseable = yes
  read only = no
  valid users = soyaos-test
  create mask = 0640
  directory mask = 0750
EOF
if ! grep -Fq 'include = /etc/samba/soyaos-e2e.conf' /etc/samba/smb.conf; then
  sudo sed -i '/^\[global\]/a\   include = /etc/samba/soyaos-e2e.conf' /etc/samba/smb.conf
fi
sudo systemctl restart smbd

sudo install -d -m 0755 /etc/exports.d
printf '%s\n' "${test_root}/nfs 127.0.0.1(rw,sync,no_subtree_check,no_root_squash,insecure)" | \
  sudo tee /etc/exports.d/soyaos-e2e.exports >/dev/null
sudo exportfs -ra
sudo systemctl restart rpcbind nfs-server

sudo a2enmod dav dav_fs >/dev/null
printf '%s:%s\n' "soyaos-test" "$(openssl passwd -apr1 "${password}")" | \
  sudo tee /etc/apache2/soyaos-e2e.htpasswd >/dev/null
if ! grep -Fq 'Listen 127.0.0.1:8088' /etc/apache2/ports.conf; then
  printf '%s\n' 'Listen 127.0.0.1:8088' | sudo tee -a /etc/apache2/ports.conf >/dev/null
fi
sudo tee /etc/apache2/sites-available/soyaos-e2e.conf >/dev/null <<'EOF'
<VirtualHost 127.0.0.1:8088>
  Alias /dav /srv/soyaos-nas-e2e/webdav
  <Directory /srv/soyaos-nas-e2e/webdav>
    DAV On
    AuthType Basic
    AuthName "SoyaOS NAS E2E"
    AuthUserFile /etc/apache2/soyaos-e2e.htpasswd
    Require valid-user
  </Directory>
</VirtualHost>
EOF
sudo a2ensite soyaos-e2e >/dev/null
sudo systemctl restart apache2

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
if ! command -v minio >/dev/null 2>&1 || ! minio --version | grep -Fq "${minio_release}"; then
  curl -fsSLo "${tmp_dir}/minio.${minio_release}" "${minio_base}/minio.${minio_release}"
  curl -fsSLo "${tmp_dir}/minio.${minio_release}.sha256sum" "${minio_base}/minio.${minio_release}.sha256sum"
  (cd "${tmp_dir}" && sha256sum -c "minio.${minio_release}.sha256sum" >/dev/null)
  sudo install -m 0755 "${tmp_dir}/minio.${minio_release}" /usr/local/bin/minio
fi

sudo install -d -m 0750 /etc/soyaos
sudo sh -c "umask 077; printf '%s\n' 'MINIO_ROOT_USER=${access_key}' 'MINIO_ROOT_PASSWORD=${secret_key}' > /etc/soyaos/minio.env"
sudo tee /etc/systemd/system/soyaos-minio.service >/dev/null <<'EOF'
[Unit]
Description=SoyaOS NAS E2E MinIO
After=network-online.target

[Service]
EnvironmentFile=/etc/soyaos/minio.env
ExecStart=/usr/local/bin/minio server --address 127.0.0.1:9000 /srv/soyaos-nas-e2e/minio
User=root
Group=root
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable soyaos-minio >/dev/null
sudo systemctl restart soyaos-minio

install -d -m 0700 "${env_dir}"
umask 077
{
  printf 'export SOYA_NAS_E2E=1\n'
  printf 'export SOYA_NAS_SMB_HOST=127.0.0.1\n'
  printf 'export SOYA_NAS_SMB_SHARE=soyaos-test\n'
  printf 'export SOYA_NAS_NFS_HOST=127.0.0.1\n'
  printf 'export SOYA_NAS_NFS_EXPORT=%q\n' "${test_root}/nfs"
  printf 'export SOYA_NAS_WEBDAV_URL=http://127.0.0.1:8088/dav\n'
  printf 'export SOYA_NAS_S3_ENDPOINT=http://127.0.0.1:9000\n'
  printf 'export SOYA_NAS_S3_BUCKET=soyaos-e2e\n'
  printf 'export SOYA_NAS_USER=soyaos-test\n'
  printf 'export SOYA_NAS_PASSWORD=%q\n' "${password}"
  printf 'export SOYA_NAS_S3_ACCESS_KEY=%q\n' "${access_key}"
  printf 'export SOYA_NAS_S3_SECRET_KEY=%q\n' "${secret_key}"
} >"${env_file}"
chmod 0600 "${env_file}"

for unit in smbd nfs-server apache2 soyaos-minio; do
  test "$(systemctl is-active "${unit}")" = "active"
done
for _ in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:9000/minio/health/live >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS http://127.0.0.1:9000/minio/health/live >/dev/null
echo "NAS E2E services ready; credentials are stored only in ${env_file}"
