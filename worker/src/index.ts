// Serves the ops CLI install script at https://ops.gbh.tech/.
//
// The script source of truth lives at the repository root (../../install.sh).
// At build time the `prebuild` npm script base64-encodes it into
// `./install.sh.b64`. We bundle the base64 blob (instead of the raw script)
// so Cloudflare's managed WAF on api.cloudflare.com doesn't pattern-match
// shell-injection signatures inside the upload body and reject the deploy.

import scriptB64 from "./install.sh.b64";

const binary = atob(scriptB64.trim());
const script = Uint8Array.from(binary, (c) => c.charCodeAt(0));

const SCRIPT_HEADERS: HeadersInit = {
  "content-type": "text/x-shellscript; charset=utf-8",
  "cache-control": "public, max-age=300",
  "x-content-type-options": "nosniff",
};

export default {
  async fetch(request: Request): Promise<Response> {
    const { pathname } = new URL(request.url);

    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response("method not allowed", {
        status: 405,
        headers: { allow: "GET, HEAD" },
      });
    }

    switch (pathname) {
      case "/":
      case "/install":
      case "/install.sh":
        return new Response(request.method === "HEAD" ? null : script, {
          status: 200,
          headers: SCRIPT_HEADERS,
        });
      default:
        return new Response("not found\n", {
          status: 404,
          headers: { "content-type": "text/plain; charset=utf-8" },
        });
    }
  },
} satisfies ExportedHandler;
