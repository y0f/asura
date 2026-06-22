// Cold-load flash regression.
//
// The app shell is server-rendered and Alpine.js loads deferred, so the page
// paints before Alpine initialises. Any sidebar element whose width/visibility
// differs between its pre-Alpine and post-Alpine state would flash/reflow on a
// cold hard refresh. This spec delays the Alpine bundle, snapshots the sidebar
// geometry on the first painted frame (double-rAF, before window.Alpine exists),
// then again once Alpine has settled, and asserts they match — for both the
// expanded and collapsed nav states.
//
// Run (Playwright required, dev-only — not part of the Go build or CI):
//   ASURA_BASE=http://127.0.0.1:8099 ASURA_KEY=ak_... node e2e/flash.spec.mjs
//
// Exits non-zero if any first-paint geometry differs from the settled geometry.
import { chromium } from 'playwright';

const BASE = process.env.ASURA_BASE || 'http://127.0.0.1:8099';
const KEY = process.env.ASURA_KEY;
const ALPINE_DELAY_MS = Number(process.env.ALPINE_DELAY_MS || 700);
if (!KEY) {
  console.error('ASURA_KEY env required (an API key for the target instance)');
  process.exit(2);
}

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 }, colorScheme: 'dark' });
const page = await ctx.newPage();
page.setDefaultTimeout(20000);

// Delay the Alpine bundle so the pre-init painted frame can be observed.
await ctx.route('**/alpine*.js', async (route) => {
  await new Promise((r) => setTimeout(r, ALPINE_DELAY_MS));
  await route.continue();
});

await page.goto(`${BASE}/login`, { waitUntil: 'domcontentloaded' });
await page.fill('input[name="api_key"]', KEY);
await Promise.all([
  page.waitForURL((u) => !u.pathname.endsWith('/login'), { timeout: 20000 }).catch(() => {}),
  page.click('button[type="submit"]'),
]);

async function firstPaintGeom(navc) {
  await page.evaluate((v) => { localStorage.setItem('navc', v); localStorage.setItem('theme', 'dark'); }, navc);
  await page.goto(`${BASE}/`, { waitUntil: 'commit' });
  return page.evaluate(() => new Promise((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => {
      const aside = document.querySelector('.app-sidebar');
      const kbd = document.querySelector('.sidebar-search-btn kbd');
      const toggle = document.querySelector('.sidebar-toggle');
      resolve({
        alpineUp: !!window.Alpine,
        sidebar: aside ? Math.round(aside.getBoundingClientRect().width) : -1,
        kbd: kbd ? Math.round(kbd.getBoundingClientRect().width) : -1,
        toggleVisible: toggle ? getComputedStyle(toggle).display !== 'none' : false,
      });
    }));
  }));
}

async function settledGeom() {
  await page.waitForFunction(() => !!window.Alpine, { timeout: 20000 });
  await page.waitForTimeout(400);
  return page.evaluate(() => {
    const aside = document.querySelector('.app-sidebar');
    const kbd = document.querySelector('.sidebar-search-btn kbd');
    return {
      sidebar: aside ? Math.round(aside.getBoundingClientRect().width) : -1,
      kbd: kbd ? Math.round(kbd.getBoundingClientRect().width) : -1,
    };
  });
}

let failures = 0;
const tol = 1;
function check(name, a, b) {
  const ok = Math.abs(a - b) <= tol;
  console.log(`${ok ? 'PASS' : 'FAIL'} ${name}: first=${a} settled=${b}`);
  if (!ok) failures++;
}

for (const navc of ['0', '1']) {
  const label = navc === '1' ? 'collapsed' : 'expanded';
  const fp = await firstPaintGeom(navc);
  if (fp.alpineUp) { console.log(`FAIL ${label}: Alpine already initialised at first paint (delay ineffective)`); failures++; }
  const settled = await settledGeom();
  check(`${label} sidebar width`, fp.sidebar, settled.sidebar);
  check(`${label} kbd width`, fp.kbd, settled.kbd);
  if (!fp.toggleVisible) { console.log(`FAIL ${label}: collapse toggle hidden at first paint`); failures++; }
}

await browser.close();
console.log(failures === 0 ? 'ALL PASS' : `${failures} FAILURE(S)`);
process.exit(failures === 0 ? 0 : 1);
