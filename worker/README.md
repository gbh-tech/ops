# ops-installer worker

Cloudflare Worker that serves the `ops` CLI install script at
`https://ops.gbh.tech/` (also reachable at `/install` and `/install.sh`).

The script itself is `install.sh` at the repo root and is bundled in at
build time via the `Text` module rule in [`wrangler.toml`](wrangler.toml), so
the Worker has no runtime dependency on GitHub.

## Local development

```bash
cd worker
npm install
npm run dev   # wrangler dev, serves on http://localhost:8787
```

Test:

```bash
curl -fsSL http://localhost:8787/install | head -n 5
```

## Deploy

To deploy manually:

```bash
cd worker
npm run check    # dry-run + typecheck
npm run deploy   # wrangler deploy
```

Requires:

- `CLOUDFLARE_API_TOKEN` — token with `Workers Scripts: Edit`,
  `Workers Routes: Edit`, and `Zone: Read` on `gbh.tech`.
- `CLOUDFLARE_ACCOUNT_ID` — target account id.
- `gbh.tech` active as a Cloudflare zone on that account (needed for
  `custom_domain = true` to bind `ops.gbh.tech`).
