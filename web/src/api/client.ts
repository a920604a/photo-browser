import type { AuthProvider } from "../auth/provider";
import type { ErrorEnvelope } from "./types";

/** A non-2xx API response, carrying the envelope code and X-Request-Id. */
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    public requestId: string | null,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ApiClient {
  getJson<T>(path: string): Promise<T>;
  getBlob(path: string, signal?: AbortSignal): Promise<Blob>;
}

/**
 * Thin fetch wrapper: attaches the bearer token, and on a 401 forces one token
 * refresh and replays the request. Everything else surfaces as an ApiError so
 * callers can branch on status (403 → forbidden screen, 404 → placeholder).
 */
export function makeApi(auth: AuthProvider, baseUrl: string): ApiClient {
  async function request(path: string, signal?: AbortSignal): Promise<Response> {
    const send = async (force: boolean) => {
      const token = await auth.getIdToken(force);
      const headers: Record<string, string> = {};
      if (token) headers.Authorization = `Bearer ${token}`;
      return fetch(baseUrl + path, { headers, signal });
    };
    let res = await send(false);
    if (res.status === 401) res = await send(true);
    if (!res.ok) throw await toApiError(res);
    return res;
  }
  return {
    getJson: async <T>(p: string) => (await request(p)).json() as Promise<T>,
    getBlob: async (p: string, signal?: AbortSignal) => (await request(p, signal)).blob(),
  };
}

async function toApiError(res: Response): Promise<ApiError> {
  const headerReqId = res.headers.get("X-Request-Id");
  try {
    // {"error":{"code","message"}} is the backend shape; a flat {code,message}
    // body is accepted too so a proxy's own error page doesn't lose detail.
    const body = (await res.json()) as ErrorEnvelope & { code?: string; message?: string };
    const code = body.error?.code ?? body.code ?? "unknown";
    const message = body.error?.message ?? body.message ?? res.statusText;
    return new ApiError(res.status, code, body.request_id ?? headerReqId, message);
  } catch {
    return new ApiError(res.status, "unknown", headerReqId, res.statusText);
  }
}
