// Static pages work without JavaScript. This only adds a remembered theme and
// keeps bookmarks from the former hash router working under any hosting path.
(() => {
  const root = document.documentElement;
  try {
    root.dataset.theme = localStorage.getItem('docs-theme') === 'dark' ? 'dark' : 'light';
  } catch (_) { /* Use the readable light default when storage is unavailable. */ }
  const pages = ['home', 'getting-started', 'configuration', 'monitors', 'notifications',
    'escalation-policies', 'sla', 'status-pages', 'api', 'backup', 'deployment', 'architecture'];
  const restoreBookmark = () => {
    const legacy = location.hash.slice(1);
    if ((location.pathname.endsWith('/') || location.pathname.endsWith('/index.html')) && pages.includes(legacy)) {
      location.replace(legacy === 'home' ? 'index.html' : legacy + '.html');
    }
  };
  restoreBookmark();
  window.addEventListener('hashchange', restoreBookmark);
  document.addEventListener('DOMContentLoaded', () => {
    const button = document.getElementById('theme-toggle');
    const label = () => button.setAttribute('aria-label', root.dataset.theme === 'dark' ? 'Use light theme' : 'Use dark theme');
    button.hidden = false;
    label();
    button.addEventListener('click', () => {
      const dark = root.dataset.theme !== 'dark';
      root.dataset.theme = dark ? 'dark' : 'light';
      try { localStorage.setItem('docs-theme', dark ? 'dark' : 'light'); } catch (_) {}
      label();
    });
  });
})();
