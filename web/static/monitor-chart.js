// withAlpha turns a CSS colour (hex or rgb) into an rgba() string with the
// given alpha, so chart fills can be derived from the theme tokens.
function withAlpha(color, a) {
    const c = document.createElement('canvas').getContext('2d');
    c.fillStyle = color;
    const hex = c.fillStyle;
    if (hex[0] !== '#') return color;
    const n = parseInt(hex.slice(1), 16);
    return 'rgba(' + ((n >> 16) & 255) + ',' + ((n >> 8) & 255) + ',' + (n & 255) + ',' + a + ')';
}
function monitorChart() {
    return {
        range: '24h',
        loading: true,
        chart: null,
        points: [],
        chartUrl: '',
        async fetchData() {
            this.loading = true;
            try {
                const resp = await fetch(this.chartUrl + '?range=' + this.range, { credentials: 'same-origin' });
                const data = await resp.json();
                this.points = data.points || [];
            } catch (e) {
                this.points = [];
            }
            this.loading = false;
            this.$nextTick(() => this.render());
        },
        render() {
            const el = this.$refs.chart;
            const tip = this.$refs.tooltip;
            if (!el || !this.points.length) return;
            if (this.chart) { this.chart.destroy(); this.chart = null; }
            const ts = this.points.map(p => p.ts);
            const rt = this.points.map(p => p.rt);
            const statuses = this.points.map(p => p.s);
            const tipDate = tip ? tip.querySelector('.tip-date') : null;
            const tipVal = tip ? tip.querySelector('.tip-value') : null;
            const tipSt = tip ? tip.querySelector('.tip-status') : null;
            const cs = getComputedStyle(document.documentElement);
            const axisColor = cs.getPropertyValue('--color-muted').trim();
            const gridColor = cs.getPropertyValue('--color-line').trim();
            const lineColor = cs.getPropertyValue('--color-chart-line').trim();
            const critColor = cs.getPropertyValue('--color-crit').trim();
            const font = '11px Inter, system-ui, sans-serif';
            const opts = {
                width: el.clientWidth, height: 200,
                cursor: { show: true, drag: { x: false, y: false } },
                select: { show: false }, legend: { show: false },
                padding: [12, 8, 0, 0],
                axes: [
                    { stroke: axisColor, grid: { stroke: gridColor, width: 1 }, ticks: { stroke: gridColor, width: 1 }, font, gap: 6 },
                    { size: 64, stroke: axisColor, grid: { stroke: gridColor, width: 1 }, ticks: { stroke: gridColor, width: 1 }, font, gap: 6, values: (u, vals) => vals.map(v => v + 'ms') }
                ],
                series: [
                    {},
                    { label: 'Response time', stroke: lineColor, width: 1.5, fill: withAlpha(lineColor, 0.1), points: { show: false } }
                ],
                hooks: {
                    draw: [(u) => {
                        const ctx = u.ctx;
                        const { left, top, width, height } = u.bbox;
                        for (let i = 0; i < statuses.length; i++) {
                            if (statuses[i] !== 'down') continue;
                            const x = Math.round(u.valToPos(ts[i], 'x', true));
                            ctx.save();
                            ctx.globalAlpha = 0.18;
                            ctx.fillStyle = critColor;
                            const bw = Math.max(2, width / statuses.length);
                            ctx.fillRect(x - bw / 2, top, bw, height);
                            ctx.restore();
                        }
                    }],
                    setCursor: [(u) => {
                        if (!tip) return;
                        const idx = u.cursor.idx;
                        if (idx == null) { tip.style.display = 'none'; return; }
                        const t = new Date(ts[idx] * 1000);
                        const time = t.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
                        const date = t.toLocaleDateString([], { month: 'short', day: 'numeric' });
                        if (tipDate) tipDate.textContent = date + ' ' + time;
                        if (tipVal) tipVal.textContent = rt[idx] + 'ms';
                        if (tipSt) { tipSt.textContent = statuses[idx] === 'down' ? 'down' : ''; tipSt.style.display = statuses[idx] === 'down' ? '' : 'none'; }
                        tip.style.display = 'block';
                        const cx = u.valToPos(ts[idx], 'x', true);
                        const tipW = tip.offsetWidth;
                        let lx = cx + 12;
                        if (lx + tipW > el.clientWidth) lx = cx - tipW - 12;
                        tip.style.left = lx + 'px';
                        tip.style.top = '8px';
                    }]
                }
            };
            this.chart = new uPlot(opts, [ts, rt], el);
        },
        resizeObs: null,
        themeHandler: null,
        init() {
            this.chartUrl = this.$el.dataset.chartUrl;
            this.fetchData();
            this.resizeObs = new ResizeObserver(() => {
                if (this.chart && this.$refs.chart) this.chart.setSize({ width: this.$refs.chart.clientWidth, height: 200 });
            });
            this.$nextTick(() => { if (this.$refs.chart) this.resizeObs.observe(this.$refs.chart); });
            this.themeHandler = () => this.render();
            window.addEventListener('theme-changed', this.themeHandler);
        },
        destroy() { if (this.resizeObs) this.resizeObs.disconnect(); if (this.chart) this.chart.destroy(); if (this.themeHandler) window.removeEventListener('theme-changed', this.themeHandler); }
    };
}
