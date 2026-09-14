# Tunnel credentials

`cloudflared tunnel create <name>` writes a JSON credential to
`~/.cloudflared/<tunnel-id>.json`. That file is the tunnel's private key.

Rules:

- It is **never** committed. `.gitignore` excludes `deploy/cloudflared/*.json`.
- It is **never** copied into an image layer. The compose file mounts it at
  runtime from `${NAS_DATA}/cloudflared/`.
- On the NAS it must be `chmod 0400` and owned by `APP_UID:APP_GID`.

Install it like this (on the NAS, after the runbook creates the tunnel):

```
mkdir -p /volume1/docker/photo-browser/data/cloudflared
cp <tunnel-id>.json /volume1/docker/photo-browser/data/cloudflared/
chown 1026:100 /volume1/docker/photo-browser/data/cloudflared/<tunnel-id>.json
chmod 0400     /volume1/docker/photo-browser/data/cloudflared/<tunnel-id>.json
```

Then replace the `REPLACE_WITH_*` placeholders in `config.yml` and check it:

```
bash scripts/verify-tunnel-config.sh deploy/cloudflared/config.yml deployed
```

To rotate: delete the tunnel in the Cloudflare dashboard, create a new one,
replace the file and the id, and restart the `cloudflared` service. Nothing
else in the deployment changes.
