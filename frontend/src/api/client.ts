export interface ApiErrorBody {
  error?: string
  requiresConfirmation?: boolean
  [key: string]: unknown
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly body: ApiErrorBody,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  const response = await fetch(path, { ...init, headers })
  const body: unknown = await response.json().catch(() => ({}))
  if (!response.ok) {
    const errorBody = isRecord(body) ? body as ApiErrorBody : {}
    throw new ApiError(errorBody.error || `请求失败（${response.status}）`, response.status, errorBody)
  }
  return body as T
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
