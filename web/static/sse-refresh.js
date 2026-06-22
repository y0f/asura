(function () {
    var marker = document.querySelector('[data-sse-url][data-sse-target]');
    if (!marker || typeof EventSource === 'undefined' || typeof htmx === 'undefined') return;
    var url = marker.dataset.sseUrl;
    var target = document.getElementById(marker.dataset.sseTarget);
    if (!url || !target) return;
    var src = new EventSource(url);
    var events = ['incident.created', 'incident.resolved', 'content.changed', 'cert.changed', 'sla.breach'];
    events.forEach(function (e) {
        src.addEventListener(e, function () { htmx.trigger(target, 'sse:refresh'); });
    });
    src.onerror = function () { src.close(); };
})();
