# Passable web

Next.js frontend for Passable. See the [project README](../README.md) for the whole system.

```sh
npm install
npm run dev          # http://localhost:3000; expects the Go API on http://localhost:8080
```

Set `NEXT_PUBLIC_API_URL` to point at another API. It is inlined at build time, so set it before `npm run build`.
