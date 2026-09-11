import { ApiError, errorForStatus } from "./errors.js";

export interface ClientConfig {
  /** Base URL of marble_jar_core, e.g. https://api.marblejar.io */
  baseUrl?: string;
  /** API key (mj_...) or session bearer token. */
  apiKey?: string;
  /** Request timeout in ms. Default 30000. */
  timeoutMs?: number;
  /** Retry attempts for retryable failures. Default 3. */
  maxRetries?: number;
  fetch?: typeof globalThis.fetch;
}

const DEFAULT_BASE_URL = "http://localhost:8080";

/** Resolves configuration from explicit options then environment variables. */
export function resolveConfig(cfg: ClientConfig = {}): Required<Omit<ClientConfig, "fetch">> & {
  fetch: typeof globalThis.fetch;
} {
  const env = (typeof process !== "undefined" ? process.env : {}) as Record<string, string | undefined>;

  const baseUrl = (cfg.baseUrl ?? env.MARBLE_JAR_BASE_URL ?? DEFAULT_BASE_URL).replace(/\/+$/, "");
  const apiKey = cfg.apiKey ?? env.MARBLE_JAR_API_KEY ?? "";
  const fetchImpl = cfg.fetch ?? globalThis.fetch;

  if (typeof fetchImpl !== "function") {
    throw new Error("No fetch implementation available; pass one via config.fetch");
  }

  return {
    baseUrl,
    apiKey,
    timeoutMs: cfg.timeoutMs ?? 30_000,
    maxRetries: cfg.maxRetries ?? 3,
    fetch: fetchImpl,
  };
}

export interface RequestOptions {
  method?: string;
  query?: Record<string, unknown>;
  body?: unknown;
  headers?: Record<string, string>;
  /** Skip retries for non-idempotent calls that lack an idempotency key. */
  retry?: boolean;
  signal?: AbortSignal;
}

/** Thin HTTP layer with timeout, typed errors, and jittered retry. */
export class HttpClient {
  readonly config: ReturnType<typeof resolveConfig>;

  constructor(cfg: ClientConfig = {}) {
    this.config = resolveConfig(cfg);
  }

  get baseUrl(): string {
    return this.config.baseUrl;
  }

  get apiKey(): string {
    return this.config.apiKey;
  }

  buildUrl(path: string, query?: Record<string, unknown>): string {
    const url = new URL(this.config.baseUrl + path);
    for (const [key, value] of Object.entries(query ?? {})) {
      if (value === undefined || value === null || value === "") continue;
      url.searchParams.set(key, String(value));
    }
    return url.toString();
  }

  async request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
    const method = opts.method ?? "GET";
    const url = this.buildUrl(path, opts.query);
    const retryable = opts.retry ?? (method === "GET" || method === "DELETE");
    const attempts = retryable ? this.config.maxRetries : 1;

    let lastError: ApiError | undefined;

    for (let attempt = 1; attempt <= attempts; attempt++) {
      try {
        return await this.once<T>(url, method, opts);
      } catch (err) {
        const apiErr =
          err instanceof ApiError ? err : new ApiError(String(err), 0);
        lastError = apiErr;

        if (!apiErr.isRetryable || attempt === attempts) throw apiErr;

        // Exponential backoff with jitter.
        const delay = Math.min(2 ** (attempt - 1) * 250, 5_000);
        await sleep(delay / 2 + Math.random() * (delay / 2));
      }
    }

    throw lastError ?? new ApiError("request failed", 0);
  }

  private async once<T>(url: string, method: string, opts: RequestOptions): Promise<T> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.config.timeoutMs);

    // Honor a caller-supplied signal alongside our timeout.
    const onAbort = () => controller.abort();
    opts.signal?.addEventListener("abort", onAbort);

    try {
      const headers: Record<string, string> = {
        Accept: "application/json",
        ...opts.headers,
      };
      if (this.config.apiKey) {
        headers["Authorization"] = `Bearer ${this.config.apiKey}`;
      }
      if (opts.body !== undefined) {
        headers["Content-Type"] = "application/json";
      }

      const res = await this.config.fetch(url, {
        method,
        headers,
        body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
        signal: controller.signal,
      });

      if (res.status === 204) return undefined as T;

      const text = await res.text();
      const parsed = text ? safeJson(text) : undefined;

      if (!res.ok) {
        const message =
          (parsed && typeof parsed === "object" && "error" in parsed
            ? String((parsed as { error: unknown }).error)
            : undefined) ?? `${method} ${url} failed with ${res.status}`;
        throw errorForStatus(res.status, message, parsed);
      }

      return parsed as T;
    } catch (err) {
      if (err instanceof ApiError) throw err;
      if (err instanceof Error && err.name === "AbortError") {
        throw new ApiError(`request timed out after ${this.config.timeoutMs}ms`, 0);
      }
      throw new ApiError(err instanceof Error ? err.message : String(err), 0);
    } finally {
      clearTimeout(timer);
      opts.signal?.removeEventListener("abort", onAbort);
    }
  }
}

function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
