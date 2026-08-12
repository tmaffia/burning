# Ollama Cloud reset timestamps

Ollama Cloud's undocumented `GET https://ollama.com/api/usage` endpoint accepts an API key as a bearer token and returns used fractions for `session` and `weekly` limits. Its response contains no reset timestamps, and its HTTP response contains no reset-related headers. The installed `pi-ollama-cloud` extension documents and validates this response shape ([source](https://github.com/fgrehm/pi-ollama-cloud/blob/main/usage.ts); [design discussion](https://github.com/fgrehm/pi-ollama-cloud/issues/42)).

Exact reset timestamps do appear in the authenticated [`ollama.com/settings`](https://ollama.com/settings) dashboard HTML. The existing `pi-ollama-cloud-usage-tracker` obtains them by extracting Chrome session cookies, fetching that page, and parsing `data-time` attributes ([source](https://github.com/Entelligentsia/pi-ollama-cloud-usage-tracker/blob/main/src/scraper.ts)). A live check confirmed that an Ollama API key cannot authenticate to the settings page: bearer and `X-API-Key` requests both redirect to `/signin`.

Ollama's public Cloud documentation describes API-key authentication but does not document a usage or reset-time endpoint ([source](https://docs.ollama.com/cloud)).

## Recommendation

Use `/api/usage` and report remaining percentages only. Do not scrape browser cookies: it adds browser and keyring dependencies, does not work cleanly on headless machines, and relies on two undocumented interfaces. Add reset timestamps if the API begins returning them.
