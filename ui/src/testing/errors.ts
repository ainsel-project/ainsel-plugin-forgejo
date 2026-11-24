// The shape the console's request() rejects with: an Error that also carries the
// HTTP `status` and, when the response body parsed as JSON, that parsed body —
// and, on the apply route, the rollback report. Tests throw this so a page
// walks the same path production does; the code under test reads it through
// asApiError() in src/api.ts, which is the same shape.
export function httpError(
  message: string,
  init: { status?: number; body?: unknown; report?: unknown },
): Error {
  return Object.assign(new Error(message), init);
}
