// Compatibility for the previous hash-based documentation URLs.
(() => {
  const pages = ['home', 'getting-started', 'configuration', 'access-control',
    'monitors', 'notifications', 'escalation-policies', 'sla', 'status-pages', 'api',
    'backup', 'deployment', 'architecture'];
  const restore = () => {
    const name = location.hash.slice(1);
    if (pages.includes(name)) location.replace(name === 'home' ? 'index.html' : name + '.html');
  };
  restore();
  window.addEventListener('hashchange', restore);
})();
