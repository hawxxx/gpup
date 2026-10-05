# GPUP dashboard

React 19 and TypeScript frontend embedded by Go through `web.FS()`. Generated `dist/` assets are included so a Go build does not require Node.

```sh
npm ci
npm test
npm run build
npm run dev
```

The development server forwards `/api` to `http://127.0.0.1:7331`. Production uses the embedded server and same-origin API. Client routes are served with the index fallback.

Live snapshots load with TanStack Query, continue over SSE, and poll every five seconds while disconnected. Charts retain at most 600 points. Requests use a virtual table with filtering and sorting. Unknown numeric values render as `—`.

Preview demo is opt-in and labels every view as simulated; all mutations and comparisons are disabled there. API bearer tokens are held in browser session storage and never included in URLs. Target credentials use environment variable names. Read-only server state disables mutation controls.

Tests cover measurement formatting, input bounds, endpoint credentials, API rejection, accessible target dialogs, and comparison deltas. Hardware validation requires actual GPU devices and is separate from interface verification.
