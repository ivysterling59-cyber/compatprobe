# CompatProbe v0.1.0

First public release of CompatProbe, a local Go CLI for diagnosing OpenAI-compatible API behavior.

## Included

- `check`, `version`, and `help` commands.
- Model listing, non-streaming Chat Completions, and streaming Chat Completions probes.
- SSE framing validation and observed completion markers, event counts, stream duration, and transport termination.
- DNS, TCP, TLS, connection reuse, TTFB, and first-event timeline observations where applicable.
- Evidence-based diagnosis rules and layered HTTP/network error classification.
- Terminal output, JSON schema v1, and Markdown reports with reproducible commands.
- Redaction of API credentials and sensitive headers and URL components.

`--stream-tokens` defaults to 256 (allowed range: 16-32768) and maps to Chat Completions `max_completion_tokens`. This budget can include visible output and reasoning tokens; it does not guarantee a visible-token count or stream duration. Larger budgets can consume more API tokens.

## Downloads

Standalone binaries are provided for Windows, Linux, and macOS, each on amd64 and arm64. `SHA256SUMS` contains the binary checksums. Source builds require Go 1.23 or newer.

## Scope and limitations

- The configured timeout is shared by the full check, including model listing, non-streaming, and streaming requests.
- Missing completion markers are recorded as observations and interpreted alongside transport evidence. Diagnosis does not establish a provider-specific root cause without supporting evidence.
- Some compatible providers may not support `max_completion_tokens` or model listing.
- Local mock tests cover clean stream completion. Real endpoint acceptance observed valid streaming followed by the configured deadline; that acceptance run did not validate real `[DONE]` plus clean EOF.
- Phase 1 does not include tool calling, Responses API, HTTP version comparison, HTML reports, or a web UI.

Supply credentials through `COMPATPROBE_API_KEY` and review reports before sharing them. See the README for commands and exit codes.
