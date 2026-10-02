# CompatProbe v0.1.1

Small usability and reporting update to CompatProbe's Phase 1 CLI.

## Changes

- Fix `compatprobe check --help` and `check -h`: show all options and defaults, exit successfully, and do not run probes or expose parsed credentials.
- Include HTTP status, observed SSE event/frame counts, completion markers, clean EOF, and transport errors in the terminal streaming summary. Missing facts display `not observed`, while observed zero and false values remain explicit.
- Improve `--verbose` with probe starts, final statuses, actual probe durations, total elapsed time, and the shared timeout budget. Progress is emitted at probe transitions on stderr, leaving JSON stdout intact. Skipped probes do not get fabricated progress durations.
- Add regression coverage for help, live progress, cancellation and error paths, report facts, and secret redaction; update usage documentation.

JSON schema v1, request payloads, probe status rules, and diagnosis semantics are unchanged. This release adds no Phase 2 features.

`--stream-tokens` defaults to 256 (allowed range: 16-32768) and maps to Chat Completions `max_completion_tokens`. This budget can include visible output and reasoning tokens; it does not guarantee a visible-token count or stream duration. Larger budgets can consume more API tokens.

## Downloads

Standalone binaries are provided for Windows, Linux, and macOS, each on amd64 and arm64. `SHA256SUMS` contains the binary checksums. Source builds require Go 1.23 or newer.

## Scope and limitations

- The configured timeout is shared by the full check, including model listing, non-streaming, and streaming requests.
- Missing completion markers are recorded as observations and interpreted alongside transport evidence. Diagnosis does not establish a provider-specific root cause without supporting evidence.
- Some compatible providers may not support `max_completion_tokens` or model listing.
- Local mock tests cover clean stream completion. The earlier real endpoint acceptance observed valid streaming followed by the configured deadline; that acceptance run did not validate real `[DONE]` plus clean EOF. No new live-provider acceptance run was performed for this patch.
- Phase 1 does not include tool calling, Responses API, HTTP version comparison, HTML reports, or a web UI.

Supply credentials through `COMPATPROBE_API_KEY` and review reports before sharing them. See the README for commands and exit codes.
