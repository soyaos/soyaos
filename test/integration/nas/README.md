# NAS wire integration environment

> [!WARNING]
> SoyaOS is under active development and has not been formally released.
> Protocols, commands, and test fixtures are unstable and may introduce
> breaking changes at any time. Do not use this environment with real data.

This directory provisions an isolated Ubuntu VM with no host filesystem mount.
SMB and NFS are reachable only on the VM's private network; WebDAV and S3 bind
to the VM loopback interface. Runtime credentials are random, remain in the VM,
and are removed with the VM.

```bash
limactl start --name=soyaos-app512 --cpus=4 --memory=6 --disk=20 template:ubuntu-24.04
limactl copy test/integration/nas/setup-linux.sh soyaos-app512:/tmp/setup-linux.sh
limactl shell soyaos-app512 -- bash /tmp/setup-linux.sh

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go test -c -tags=integration -o /tmp/nas-e2e.test ./pkg/connectors/nas
limactl copy /tmp/nas-e2e.test soyaos-app512:/tmp/nas-e2e.test
limactl shell soyaos-app512 -- bash -lc \
  'source ~/.config/soyaos/nas-e2e.env && /tmp/nas-e2e.test -test.v -test.run TestNASWireIntegration'

limactl delete --force soyaos-app512
```

The test does not treat a reachable port as success. Every protocol writes a
non-empty probe and reads the exact bytes back through the corresponding wire
protocol. The S3 object is removed after verification; the VM deletion removes
all remaining test files, accounts, and credentials.
