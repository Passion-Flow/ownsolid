// A typed client for one OpenAPI contract. Paths, parameters, bodies and results all come
// from the generated types in gen/, so a call that disagrees with the contract fails to
// compile. JSON endpoints only: file transfers and event streams keep their own code.
import { get, write, type RequestOptions, type WriteMethod } from './transport.ts';

type HttpMethod = 'get' | 'post' | 'put' | 'patch' | 'delete';
type SuccessStatus = 200 | 201 | 202 | 204;

/** Paths of a contract that have the given method. */
export type PathsWith<P, M extends HttpMethod> = {
  [K in keyof P]: P[K] extends Record<M, { responses: unknown }> ? K : never;
}[keyof P];

type Operation<P, K extends keyof P, M extends HttpMethod> =
  P[K] extends Record<M, infer O> ? O : never;

type PathOption<O> = O extends { parameters: { path: infer T } } ? { path: T } : unknown;

type QueryOption<O> = O extends { parameters: { query: infer T } }
  ? { query: T }
  : O extends { parameters: { query?: infer T } }
    ? [NonNullable<T>] extends [never]
      ? unknown
      : { query?: NonNullable<T> | undefined }
    : unknown;

type BodyOption<O> = O extends { requestBody: { content: { 'application/json': infer B } } }
  ? { body: B }
  : O extends { requestBody?: { content: { 'application/json': infer B } } }
    ? [B] extends [never]
      ? unknown
      : unknown extends B
        ? unknown
        : { body?: B | undefined }
    : unknown;

// A contract that requires the header requires the key, so a retry cannot apply twice.
type KeyOption<O> = O extends { parameters: { header: { 'Idempotency-Key': unknown } } }
  ? { idempotencyKey: string }
  : { idempotencyKey?: string | undefined };

/** The options of one operation: the parameters its contract requires, and a signal. */
export type Options<O> = PathOption<O> &
  QueryOption<O> &
  BodyOption<O> &
  KeyOption<O> & { signal?: AbortSignal | undefined; background?: boolean | undefined };

type Args<O> = object extends Options<O> ? [options?: Options<O>] : [options: Options<O>];

type Payload<R> = R extends { content: { 'application/json': { data: infer D } } }
  ? D
  : R extends { content?: never }
    ? undefined
    : never;

/** What an operation resolves to: the `data` of its success envelope. */
export type Success<O> = O extends { responses: infer R }
  ? { [C in keyof R & SuccessStatus]: Payload<R[C]> }[keyof R & SuccessStatus]
  : never;

type Call<P, M extends HttpMethod> = <K extends PathsWith<P, M>>(
  path: K,
  ...args: Args<Operation<P, K, M>>
) => Promise<Success<Operation<P, K, M>>>;

/** A client for the contract `P`. */
export interface Client<P> {
  get: Call<P, 'get'>;
  post: Call<P, 'post'>;
  put: Call<P, 'put'>;
  patch: Call<P, 'patch'>;
  delete: Call<P, 'delete'>;
}

type QueryValue = string | number | boolean | null | undefined;

/** Query parameters; an array repeats the parameter. */
export type Query = Readonly<Record<string, QueryValue | readonly QueryValue[]>>;

/** Options as the runtime sees them, once the types have done their work. */
interface LooseOptions extends RequestOptions {
  path?: Record<string, string | number>;
  query?: Query;
  body?: unknown;
}

function queryString(query: Query): string {
  const search = new URLSearchParams();
  for (const [name, value] of Object.entries(query)) {
    const items: readonly QueryValue[] = typeof value === 'object' && value ? value : [value];
    for (const item of items) {
      if (item === undefined || item === null) continue;
      search.append(name, String(item));
    }
  }
  const text = search.toString();
  return text === '' ? '' : `?${text}`;
}

/** Fills `{name}` path parameters and appends the query string. */
export function buildUrl(
  template: string,
  path: Record<string, string | number> = {},
  query: Query = {},
): string {
  const filled = template.replace(/\{([^}]+)\}/g, (_, name: string) => {
    const value = path[name];
    if (value === undefined) throw new Error(`missing path parameter ${name} for ${template}`);
    return encodeURIComponent(String(value));
  });
  return filled + queryString(query);
}

function requestOptions({ signal, idempotencyKey, background }: LooseOptions): RequestOptions {
  return {
    ...(signal ? { signal } : {}),
    ...(idempotencyKey ? { idempotencyKey } : {}),
    ...(background ? { background } : {}),
  };
}

function writer(method: WriteMethod) {
  return (path: string, options: LooseOptions = {}) =>
    write(
      method,
      buildUrl(path, options.path, options.query),
      options.body,
      requestOptions(options),
    );
}

/** Creates a client for a generated contract: `createClient<identityContract.paths>()`. */
export function createClient<P>(): Client<P> {
  return {
    get: (path: string, options: LooseOptions = {}) =>
      get(buildUrl(path, options.path, options.query), requestOptions(options)),
    post: writer('POST'),
    put: writer('PUT'),
    patch: writer('PATCH'),
    delete: writer('DELETE'),
  } as unknown as Client<P>;
}
