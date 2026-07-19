# Phase 1 Incus compatibility spike

This nested module is disposable evidence for Phase 1. It does not implement the
application, S3 publishing, proxy behavior, or template rebranding.

The generator creates one design-conforming split-VM mirror with:

- a content-addressed metadata tarball and QCOW2 disk;
- an Incus product document accepted by `schema/incus.ValidateRuntimeProductFile`;
- exactly `incus.tar.xz` and `disk-kvm.img` items;
- the metadata-first combined SHA-256 fingerprint; and
- an index pointing to a content-addressed product snapshot.

The proof uses a disposable Lima Linux VM for the Incus daemon and `mkcert` for
the HTTPS certificate. Exact commands and observed results are recorded in
`EVIDENCE.md` after the run.

Run the isolated generator test with:

```console
go test ./...
```

