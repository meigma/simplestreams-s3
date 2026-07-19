# Technical Notes

- V1 implementation is governed by the [session 001 design](001/DESIGN.md) and its [companion plan](001/PLAN.md). Execute the plan in order, beginning with the disposable compatibility proof; the design prevails when the plan summarizes a requirement.
- Use hexagonal architecture at all times. Keep business logic isolated from CLI, filesystem, network, storage, and other external adapters.
- Prefer functional testing before calling any feature complete. Unit tests are useful, but they do not prove the tool works the way the design intends.
- Take an agile approach to development. Avoid waterfall: underspecify when useful, prototype early, learn from the result, and refine from working behavior.
