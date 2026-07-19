# Phase 1 Incus compatibility evidence

Date: 2026-07-18 PDT

## Result

The Phase 1 compatibility proof passed. `go-simplestreams` v0.1.0 generated a
two-item split-VM catalog that its Incus runtime schema accepted. Incus listed
the expected alias through locally trusted HTTPS, imported the image, and
reported the exact metadata-first combined SHA-256 fingerprint.

No observed behavior conflicts with the V1 design.

## Environment

```text
host: macOS 26.4 arm64
go: go1.26.4 darwin/arm64
lima: 2.0.3, VZ aarch64 Ubuntu 24.04 guest
mkcert: 1.4.4
Incus guest client/server: 6.0.0 / 6.0.0
go-simplestreams: v0.1.0
golang.org/x/net selected override: v0.57.0
```

Kusari's first analysis found that `go-simplestreams` v0.1.0 selected
`golang.org/x/net` v0.52.0 through CUE and flagged CVE-2026-39821. The spike
retains the required library version while selecting the fixed `x/net` v0.57.0.
The generator tests and full Incus listing/import proof were repeated after
that dependency update with identical catalog paths and fingerprint.

Kusari also reported `github.com/opencontainers/go-digest` v1.0.0 as carrying
CC-BY-SA-4.0. The module's Go source is Apache-2.0; its README explicitly limits
the Creative Commons license to `README.md` and `CONTRIBUTING.md`. No dependency
source or documentation is distributed by this unmerged spike.

## Source image

The proof used the current public Alpine 3.22 arm64 cloud VM artifacts from
`images.linuxcontainers.org`, version `20260718_13:01`:

```text
metadata URL: https://images.linuxcontainers.org/images/alpine/3.22/arm64/cloud/20260718_13:01/incus.tar.xz
metadata size: 1336
metadata SHA-256: f813852fbb7bf1c35985d2c35ffc0f2b8b9380ad3a8bbfa6707227dc434c09fe

disk URL: https://images.linuxcontainers.org/images/alpine/3.22/arm64/cloud/20260718_13:01/disk.qcow2
disk size: 89880576
disk SHA-256: 3dda50f6c64a3be5bb2b917eda5d102872909d98d295971331ddf79357f4eb84

metadata creation_date: 1784379772
metadata properties.os: alpinelinux
metadata properties.release: 3.22
metadata properties.variant: cloud
metadata architecture: aarch64
```

## Reproduction

From `spike/phase1`:

```console
limactl start \
  --name=simplestreams-phase1 \
  --cpus=4 --memory=6 --disk=20 \
  --vm-type=vz --containerd=none --mount-writable \
  --timeout=10m --progress -y template:ubuntu-lts

limactl shell simplestreams-phase1 -- bash -lc \
  'sudo apt-get update && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y incus xz-utils ca-certificates jq'

limactl shell simplestreams-phase1 -- bash -lc \
  'sudo incus admin init --minimal'

mkdir -p artifacts
curl -fL --retry 3 -o artifacts/incus.tar.xz \
  https://images.linuxcontainers.org/images/alpine/3.22/arm64/cloud/20260718_13:01/incus.tar.xz
curl -fL --retry 3 -o artifacts/disk.qcow2 \
  https://images.linuxcontainers.org/images/alpine/3.22/arm64/cloud/20260718_13:01/disk.qcow2

go test -count=1 ./...
go run ./cmd/generate \
  -metadata artifacts/incus.tar.xz \
  -disk artifacts/disk.qcow2 \
  -output mirror \
  -os alpinelinux \
  -release 3.22 \
  -variant cloud \
  -architecture arm64 \
  -creation-date 1784379772

mkdir -p certs
mkcert -install
mkcert -cert-file certs/phase1.pem -key-file certs/phase1-key.pem \
  host.lima.internal localhost 127.0.0.1 ::1

mkcert_ca_root="$(mkcert -CAROOT)"
limactl shell simplestreams-phase1 -- \
  sudo cp "$mkcert_ca_root/rootCA.pem" /usr/local/share/ca-certificates/mkcert.crt
limactl shell simplestreams-phase1 -- bash -lc \
  'sudo update-ca-certificates && sudo systemctl restart incus.service'

go run ./cmd/serve \
  -listen :8443 \
  -root mirror \
  -cert certs/phase1.pem \
  -key certs/phase1-key.pem
```

With the HTTPS server running, the Incus proof commands were:

```console
limactl shell simplestreams-phase1 -- bash -lc \
  'sudo incus remote add phase1 https://host.lima.internal:8443 --protocol simplestreams'

limactl shell simplestreams-phase1 -- bash -lc \
  'sudo incus image list phase1: --format json | jq -c '\''.[] | select(any(.aliases[]?; .name == "alpinelinux/3.22/cloud")) | {fingerprint,aliases,architecture,type,size}'\'''

limactl shell simplestreams-phase1 -- bash -lc \
  'sudo incus image copy phase1:alpinelinux/3.22/cloud local: --vm --alias phase1-imported'

limactl shell simplestreams-phase1 -- bash -lc \
  'sudo incus image list local: phase1-imported --format json | jq -c '\''.[0] | {fingerprint,aliases,architecture,type,size}'\'''
```

## Generated contract

The generator calls `schema/incus.ValidateRuntimeProductFile` before rendering.
The successful run produced:

```json
{
  "product": "alpinelinux:3.22:cloud:arm64",
  "alias": "alpinelinux/3.22/cloud",
  "version": "202607181302",
  "architecture": "arm64",
  "fingerprint": "3f16ca76d823d3ba62d2ca3d58de3e7909053bd569805aff45c9e2c3554fae25",
  "metadata_path": "images/f813852fbb7bf1c35985d2c35ffc0f2b8b9380ad3a8bbfa6707227dc434c09fe.incus.tar.xz",
  "disk_path": "images/3dda50f6c64a3be5bb2b917eda5d102872909d98d295971331ddf79357f4eb84.qcow2",
  "product_path": "streams/v1/images-e1f9f044e57582362357f805e5b8233b1c428b2f187fb9db9346c4a1d756db51.json"
}
```

The product document contained exactly:

```text
incus.tar.xz  ftype=incus.tar.xz  combined_disk-kvm-img_sha256=3f16ca76d823d3ba62d2ca3d58de3e7909053bd569805aff45c9e2c3554fae25
disk-kvm.img   ftype=disk-kvm.img
```

## Incus observations

Listing through the proof remote returned:

```json
{"fingerprint":"3f16ca76d823d3ba62d2ca3d58de3e7909053bd569805aff45c9e2c3554fae25","aliases":[{"name":"alpinelinux/3.22/cloud","description":""},{"name":"alpinelinux/3.22/cloud/arm64","description":""}],"architecture":"aarch64","type":"virtual-machine","size":89881912}
```

The imported local image returned:

```json
{"fingerprint":"3f16ca76d823d3ba62d2ca3d58de3e7909053bd569805aff45c9e2c3554fae25","aliases":[{"name":"phase1-imported","description":""}],"architecture":"aarch64","type":"virtual-machine","size":89881912}
```

The expected and imported fingerprints were byte-for-byte equal:

```text
expected=3f16ca76d823d3ba62d2ca3d58de3e7909053bd569805aff45c9e2c3554fae25
actual=3f16ca76d823d3ba62d2ca3d58de3e7909053bd569805aff45c9e2c3554fae25
```

## Conclusion

The design's product name, default alias, compact UTC version identity,
content-addressed paths, item names, item file types, checksums, and
metadata-first combined fingerprint are accepted by Incus for listing and VM
import. Phase 2 may proceed without changing the marked wire assumptions.

The spike branch and draft PR are intentionally closed without merge after this
evidence is reviewed; no spike code is production input.

After the evidence run, stop the HTTPS process and remove the disposable guest:

```console
limactl stop simplestreams-phase1
limactl delete -f simplestreams-phase1
```
