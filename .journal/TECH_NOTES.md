# Technical Notes

- V1 implementation is governed by the [session 001 design](001/DESIGN.md) and its [companion plan](001/PLAN.md). Execute the plan in order, beginning with the disposable compatibility proof; the design prevails when the plan summarizes a requirement.
- Use hexagonal architecture at all times. Keep business logic isolated from CLI, filesystem, network, storage, and other external adapters.
- Prefer functional testing before calling any feature complete. Unit tests are useful, but they do not prove the tool works the way the design intends.
- Take an agile approach to development. Avoid waterfall: underspecify when useful, prototype early, learn from the result, and refine from working behavior.
- Local development hosts have Lima and `mkcert` available. Prefer Lima for disposable Linux/Incus environments and `mkcert` for locally trusted HTTPS certificates when functional proofs need them.
- Phase 1 passed in closed, unmerged PR #7: Incus listed and imported the designed two-item VM catalog, and the imported fingerprint exactly matched the metadata-first combined SHA-256. Phase 2 preserved that wire contract and merged through PR #8 as `868a29c`.
- When `go-simplestreams` v0.1.0 enters the production module, select `golang.org/x/net` v0.57.0 or newer; Kusari flagged its transitive v0.52.0 for CVE-2026-39821. After installing a `mkcert` CA in a running Lima guest, restart Incus so it reloads the system certificate pool.
- Phase 2 production support is AWS S3 only and intentionally refuses non-empty mirrors. MinIO is used solely by the build-tagged `internal/integration` process test through hidden endpoint, path-style, and single-PUT test hooks; safe repeat publication belongs to Phase 3.
- MinIO rejected the AWS transfer manager's multipart CRC-64/NVME completion with `InvalidPart`, so real-AWS multipart behavior remains an optional future conformance test when a disposable private bucket is available.
- Keep integration infrastructure in scenario helpers but keep behavior and assertions in categorized process files; share one Testcontainers dependency across the full procedure to avoid resource churn.
