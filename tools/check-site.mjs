// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const site = path.join(repo, 'site');
const read = (rel) => fs.readFileSync(path.join(repo, rel), 'utf8');
const fail = (message) => {
  process.stderr.write(`site check failed: ${message}\n`);
  process.exit(1);
};
const expect = (condition, message) => {
  if (!condition) fail(message);
};

const requiredFiles = [
  'site/index.html',
  'site/404.html',
  'site/robots.txt',
  'site/sitemap.xml',
  'site/.nojekyll',
  'site/assets/site.css',
  'site/assets/site.js',
  'site/assets/fridge-mark.svg',
  'site/assets/before-after.svg',
  'site/assets/protocol-flow.svg',
  'site/docs/assets/social-preview.png',
];
requiredFiles.forEach((rel) => expect(fs.existsSync(path.join(repo, rel)), `missing ${rel}`));

const themeScript = `    (() => {
      const param = new URLSearchParams(window.location.search).get("clawpilotTheme");
      const theme =
        param || (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
      document.documentElement.setAttribute("data-theme", theme);
    })();`;

const themeVariables = `:root {
      color-scheme: light;
      --cp-bg: #f7f4ef;
      --cp-bg-elevated: #fcfbf8;
      --cp-surface: #ffffff;
      --cp-surface-soft: #f5f5f5;
      --cp-border: #dedede;
      --cp-border-strong: #919191;
      --cp-text: #242424;
      --cp-text-muted: #5c5c5c;
      --cp-text-soft: #6f6f6f;
      --cp-accent: #b11f4b;
      --cp-accent-hover: #9a1a41;
      --cp-accent-soft: rgba(177, 31, 75, 0.08);
      --cp-accent-fg: #ffffff;
      --cp-success: #16a34a;
      --cp-danger: #dc2626;
      --cp-warning: #f59e0b;
      --cp-link: #0078d4;
      --cp-shadow: 0 18px 48px rgba(0, 0, 0, 0.12);
      --cp-overlay: rgba(255, 255, 255, 0.8);
      --cp-panel: rgba(255, 255, 255, 0.86);
      --cp-panel-strong: rgba(255, 255, 255, 0.96);
      --cp-sheen: rgba(255, 255, 255, 0.55);
      --cp-highlight: rgba(177, 31, 75, 0.12);
    }
    html[data-theme="dark"] {
      color-scheme: dark;
      --cp-bg: #3d3b3a;
      --cp-bg-elevated: #343231;
      --cp-surface: #292929;
      --cp-surface-soft: #2e2e2e;
      --cp-border: #474747;
      --cp-border-strong: #5f5f5f;
      --cp-text: #dedede;
      --cp-text-muted: #919191;
      --cp-text-soft: #b0b0b0;
      --cp-accent: #fd8ea1;
      --cp-accent-hover: #fb7b91;
      --cp-accent-soft: rgba(253, 142, 161, 0.14);
      --cp-accent-fg: #1a1a1a;
      --cp-success: #4ade80;
      --cp-danger: #f87171;
      --cp-warning: #fbbf24;
      --cp-link: #4da6ff;
      --cp-shadow: 0 18px 48px rgba(0, 0, 0, 0.32);
      --cp-overlay: rgba(41, 41, 41, 0.88);
      --cp-panel: rgba(41, 41, 41, 0.72);
      --cp-panel-strong: rgba(41, 41, 41, 0.96);
      --cp-sheen: rgba(255, 255, 255, 0.04);
      --cp-highlight: rgba(253, 142, 161, 0.12);
    }`;

const css = read('site/assets/site.css');

for (const rel of ['site/index.html', 'site/404.html']) {
  const html = read(rel);
  const scripts = [...html.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/g)];
  expect(scripts.length > 0, `${rel} has no theme script`);
  expect(scripts[0][1].trimEnd() === `\n${themeScript}\n  `.trimEnd(), `${rel} changed the mandatory theme script`);
  expect(html.includes(themeVariables), `${rel} changed the mandatory theme variables`);
  expect(
    html.includes('"Segoe UI", Aptos, Calibri, -apple-system, BlinkMacSystemFont, sans-serif')
      || css.includes('"Segoe UI", Aptos, Calibri, -apple-system, BlinkMacSystemFont, sans-serif'),
    `${rel} lost Segoe UI typography`,
  );
  expect(!html.includes('font.googleapis.com') && !html.includes('fonts.gstatic.com'), `${rel} loads an external font`);
  expect(![...html].some((char) => char.codePointAt(0) > 127), `${rel} is not ASCII clean`);

  const withoutVariables = html.replace(themeVariables, '');
  expect(!/#[0-9a-fA-F]{3,8}|rgba?\(|hsla?\(/.test(withoutVariables), `${rel} hardcodes a component color`);
}

const index = read('site/index.html');
const js = read('site/assets/site.js');

expect(!/#[0-9a-fA-F]{3,8}|rgba?\(|hsla?\(/.test(css), 'site.css hardcodes a component color');
expect(css.includes('font-family: "Segoe UI", Aptos, Calibri, -apple-system, BlinkMacSystemFont, sans-serif'), 'site.css lost required typography');
expect(css.includes('@media (prefers-reduced-motion: reduce)'), 'site.css has no reduced-motion treatment');
expect(![...css, ...js].some((char) => char.codePointAt(0) > 127), 'site CSS or JavaScript is not ASCII clean');
expect(!/<script[^>]+src=["']https?:\/\//.test(index), 'index loads external JavaScript');
expect(!/<link[^>]+href=["']https?:\/\/[^"']+\.(css|woff2?)/.test(index), 'index loads an external stylesheet or font');

const requiredOrder = [
  'id="top"',
  'id="problem"',
  'id="incident"',
  'id="protocol"',
  'id="mental-model"',
  'id="workspace"',
  'id="architecture"',
  'id="transcript"',
  'id="compatibility"',
  'id="collaborate"',
  'id="compare"',
  'id="install"',
  'id="security"',
  'id="contribute"',
];
let previous = -1;
for (const marker of requiredOrder) {
  const at = index.indexOf(marker);
  expect(at > previous, `narrative marker missing or out of order: ${marker}`);
  previous = at;
}

expect(index.includes('https://ragnarpitla.github.io/agent-fridge/docs/assets/social-preview.png'), 'Open Graph image is not the final Pages URL');
expect(index.includes('data-trigger-collision'), 'collision interaction is missing');
expect(index.includes('data-copy-target'), 'copy controls are missing');
expect(index.includes('role="tablist"') && index.includes('role="tabpanel"'), 'terminal pane tabs are missing');

const ids = [...index.matchAll(/\sid="([^"]+)"/g)].map((match) => match[1]);
expect(new Set(ids).size === ids.length, 'index has duplicate ids');
for (const href of index.matchAll(/href="#([^"]+)"/g)) {
  expect(ids.includes(href[1]), `broken section link #${href[1]}`);
}

const localRefs = [...index.matchAll(/(?:src|href)="([^"]+)"/g)]
  .map((match) => match[1])
  .filter((ref) => !/^(?:https?:|#|mailto:)/.test(ref));
for (const ref of localRefs) {
  const pathname = ref.split(/[?#]/, 1)[0];
  expect(fs.existsSync(path.join(site, pathname)), `broken local reference ${ref}`);
}

expect(
  fs.readFileSync(path.join(repo, 'docs/assets/before-after.svg')).equals(fs.readFileSync(path.join(site, 'assets/before-after.svg'))),
  'deployed before-after.svg drifted from docs source',
);
expect(
  fs.readFileSync(path.join(repo, 'docs/assets/protocol-flow.svg')).equals(fs.readFileSync(path.join(site, 'assets/protocol-flow.svg'))),
  'deployed protocol-flow.svg drifted from docs source',
);
expect(
  fs.readFileSync(path.join(repo, 'docs/assets/social-preview.png')).equals(fs.readFileSync(path.join(site, 'docs/assets/social-preview.png'))),
  'deployed social preview drifted from docs source',
);

const preview = fs.readFileSync(path.join(site, 'docs/assets/social-preview.png'));
expect(preview.length < 1_000_000, 'social preview exceeds 1 MB');
expect(preview.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])), 'social preview is not a PNG');
expect(preview.readUInt32BE(16) === 1280 && preview.readUInt32BE(20) === 640, 'social preview is not 1280x640');

const screenshots = [
  ['docs/assets/site/desktop-hero.png', 1440, 1000],
  ['docs/assets/site/four-agent-scenario.png', 1440, 1200],
  ['docs/assets/site/mobile-article.png', 375, 812],
];
for (const [rel, width, height] of screenshots) {
  const png = fs.readFileSync(path.join(repo, rel));
  expect(png.readUInt32BE(16) === width && png.readUInt32BE(20) === height, `${rel} has unexpected dimensions`);
}

process.stdout.write(`site check: ${requiredFiles.length} publication files, ${requiredOrder.length} ordered sections, ${ids.length} unique ids, and 3 browser proofs verified\n`);
