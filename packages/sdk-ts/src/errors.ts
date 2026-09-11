/** Typed errors so callers can branch on failure mode instead of parsing strings. */

export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;

  constructor(message: string, status: number, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }

  /** Network failures and timeouts surface as status 0. */
  get isNetworkError(): boolean {
    return this.status === 0;
  }

  /** 5xx, 429 and network failures are worth retrying. */
  get isRetryable(): boolean {
    return this.status === 0 || this.status === 429 || this.status >= 500;
  }
}

export class ValidationError extends ApiError {
  constructor(message: string, body?: unknown) {
    super(message, 400, body);
    this.name = "ValidationError";
  }
}

export class AuthError extends ApiError {
  constructor(message: string, status = 401, body?: unknown) {
    super(message, status, body);
    this.name = "AuthError";
  }
}

export class NotFoundError extends ApiError {
  constructor(message: string, body?: unknown) {
    super(message, 404, body);
    this.name = "NotFoundError";
  }
}

export class ConflictError extends ApiError {
  constructor(message: string, body?: unknown) {
    super(message, 409, body);
    this.name = "ConflictError";
  }
}

export function errorForStatus(status: number, message: string, body?: unknown): ApiError {
  switch (status) {
    case 400:
      return new ValidationError(message, body);
    case 401:
    case 403:
      return new AuthError(message, status, body);
    case 404:
      return new NotFoundError(message, body);
    case 409:
      return new ConflictError(message, body);
    default:
      return new ApiError(message, status, body);
  }
}
