import { writeFileSync } from "node:fs";
import { defineConfig, type Plugin } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// The Go binary embeds frontend/dist via go:embed, and a fresh clone has no
// build output: the .gitkeep keeps the directory non-empty so `go build` works
// before the first `npm run build`. Vite empties the directory on every build,
// so put the marker back when the build finishes.
function keepGitkeep(): Plugin {
  return {
    name: "keep-gitkeep",
    closeBundle() {
      writeFileSync(new URL("./dist/.gitkeep", import.meta.url), "");
    },
  };
}

// The Go server serves the built assets; during development it proxies the API
// to a running `go run . -addr 127.0.0.1:8080`.
export default defineConfig({
  plugins: [svelte(), keepGitkeep()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
      },
    },
  },
  build: {
    target: "es2020",
  },
});
