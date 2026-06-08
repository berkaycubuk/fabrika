import * as Sentry from "@sentry/browser";

export function initObservability(): void {
  Sentry.init({
    dsn: "https://128519b05f9c2f238ac379c386efe4cb@o4506500501340160.ingest.us.sentry.io/4511529648455680",
  });
}
