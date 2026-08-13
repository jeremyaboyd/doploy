// Container healthcheck. Exits 0 when the API answers, 1 otherwise.
//
// Deliberately dependency-free so it can run with plain node in the image.

const port = Number(process.env.PORT || 8080);

const request = await fetch(`http://127.0.0.1:${port}/api/healthz`, {
  signal: AbortSignal.timeout(2500),
}).catch(() => null);

if (!request || !request.ok) {
  process.exit(1);
}
process.exit(0);
