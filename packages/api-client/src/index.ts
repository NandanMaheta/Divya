import createClient from "openapi-fetch";
import type { paths } from "./schema";

export type { components, paths } from "./schema";

/** Typed client generated from the Go API's OpenAPI spec (`pnpm --filter @divya/api-client generate`). */
export function createApiClient(baseUrl: string, getToken?: () => string | undefined) {
  const client = createClient<paths>({ baseUrl });
  client.use({
    onRequest({ request }) {
      const token = getToken?.();
      if (token) request.headers.set("Authorization", `Bearer ${token}`);
      return request;
    },
  });
  return client;
}

export type ApiClient = ReturnType<typeof createApiClient>;
