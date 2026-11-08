(function () {
    if (typeof EventSource === 'undefined' || typeof htmx === 'undefined') return;
    if (window.__asuraSSEController) {
        window.__asuraSSEController.sync();
        return;
    }

    var source = null, url = '';
    function marker() {
        return document.querySelector('[data-sse-url][data-sse-target]');
    }
    function close() {
        if (source) source.close();
        source = window.__asuraSSE = null;
        url = '';
    }
    function sync() {
        var el = marker();
        if (!el || !el.dataset.sseUrl || !document.getElementById(el.dataset.sseTarget)) {
            close();
            return;
        }
        if (source && url === el.dataset.sseUrl) return;
        close();
        url = el.dataset.sseUrl;
        var connection = source = window.__asuraSSE = new EventSource(url);
        ['incident.created', 'incident.resolved', 'content.changed', 'cert.changed', 'sla.breach'].forEach(function (event) {
            connection.addEventListener(event, function () {
                if (source !== connection) return;
                var current = marker();
                var target = current && document.getElementById(current.dataset.sseTarget);
                // Refreshes replace the target, so resolve it for each event.
                if (target) htmx.trigger(target, 'sse:refresh');
            });
        });
        // EventSource reconnects after transient errors; polling remains a fallback.
    }

    // Boosted navigation can execute this script again without replacing the shell.
    window.__asuraSSEController = { sync: sync };
    document.addEventListener('htmx:afterSwap', sync);
    document.addEventListener('htmx:historyRestore', sync);
    window.addEventListener('pagehide', close);
    window.addEventListener('pageshow', sync);
    sync();
})();
