# Agent Fridge website

The public technical article is a dependency-free static site under [`site/`](../site/).
GitHub Pages publishes that directory without a framework or build step.

Expected production URL:
<https://ragnarpitla.github.io/agent-fridge/>

## Preview locally

From the repository root:

```bash
python3 -m http.server 41737 --directory site
```

Open:

```text
http://127.0.0.1:41737/
```

Theme checks:

```text
http://127.0.0.1:41737/?clawpilotTheme=light
http://127.0.0.1:41737/?clawpilotTheme=dark
```

The manual theme toggle updates the `clawpilotTheme` query parameter while
preserving any other query values and the current section hash.

Run the static publication gate before serving or publishing:

```bash
npm run site:check
```

## Publication layout

| Path | Purpose |
| --- | --- |
| `site/index.html` | Long-form technical publication |
| `site/404.html` | Project-aware GitHub Pages fallback |
| `site/assets/site.css` | Shared responsive Clawpilot-theme styles |
| `site/assets/site.js` | Theme, progress, navigation, copy, and workspace interactions |
| `site/assets/fridge-mark.svg` | Favicon and local project mark |
| `site/assets/before-after.svg` | Deployed copy of the before/after visual |
| `site/assets/protocol-flow.svg` | Deployed copy of the protocol visual |
| `site/docs/assets/social-preview.png` | Open Graph and Twitter preview at the final Pages path |
| `site/robots.txt` | Search crawler policy |
| `site/sitemap.xml` | Canonical public URL |

The source visuals remain under `docs/assets/`. When a source asset changes,
refresh its deployed copy under `site/` and validate both.

## Deployment

`.github/workflows/pages.yml` runs on pushes to `main` and manual dispatch. It:

1. Checks out the repository.
2. Verifies the required static files.
3. Configures GitHub Pages with `actions/configure-pages`.
4. Uploads only `site/` with `actions/upload-pages-artifact`.
5. Deploys with `actions/deploy-pages`.

The workflow has only the permissions GitHub Pages requires: read repository
contents, write Pages, and mint an OIDC token.

Before the first deployment, a repository administrator may need to open
**Settings -> Pages** and select **GitHub Actions** as the source. This pull
request does not change repository settings and does not deploy before merge.

## Validation checklist

- Serve the exact `site/` directory.
- Check light, dark, and query-forced themes.
- Exercise the terminal tabs, collision toggle, and command copy buttons.
- Verify all internal links and local assets return HTTP 200.
- Check widths 375, 768, 1280, and 1440 pixels.
- Confirm no console errors, network failures, horizontal overflow, or missing
  accessible names.
- Regenerate the committed evidence under `docs/assets/site/` when the layout
  changes materially.

## Committed browser evidence

- [`docs/assets/site/desktop-hero.png`](assets/site/desktop-hero.png) - desktop
  hero at 1440x1000.
- [`docs/assets/site/four-agent-scenario.png`](assets/site/four-agent-scenario.png)
  - dark-theme four-lane workspace with the exit-10 collision triggered.
- [`docs/assets/site/mobile-article.png`](assets/site/mobile-article.png) -
  mobile incident article at 375x812.
