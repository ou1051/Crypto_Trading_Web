(() => {
    'use strict';

    const app = document.getElementById('chart_app');
    const status = document.getElementById('chart_status');
    const container = document.getElementById('dashboard_div');
    const buttons = [...app.querySelectorAll('button[data-duration]')];
    const refreshInterval = 3000;
    const requestTimeout = 15000;
    let duration = app.dataset.duration;
    let dashboard;
    let rangeControl;
    let dataTable;
    let selectedRange = null;
    let bounds;
    let drawing = false;
    let hovering = false;
    let focused = false;
    let stopped = false;
    let timer;
    let controller;
    let requestID = 0;

    function message(text, error = false) {
        status.textContent = text;
        status.dataset.error = String(error);
    }

    function cancelRequest() {
        clearTimeout(timer);
        requestID++;
        if (controller) controller.abort();
        controller = null;
    }

    function paused() {
        return hovering || focused || document.hidden;
    }

    function schedule() {
        clearTimeout(timer);
        if (!stopped && !paused()) timer = setTimeout(refresh, refreshInterval);
    }

    function rememberRange() {
        if (drawing || !bounds) return;
        const range = rangeControl.getState().range;
        if (!range) return;
        const start = range.start.getTime();
        const end = range.end.getTime();
        selectedRange = start <= bounds.start && end >= bounds.end ? null : {
            start, end, followLatest: end >= bounds.end
        };
    }

    function draw() {
        if (!dataTable || dataTable.getNumberOfRows() === 0 || stopped) return;
        const start = dataTable.getValue(0, 0).getTime();
        const end = dataTable.getValue(dataTable.getNumberOfRows() - 1, 0).getTime();
        bounds = {start, end};
        let rangeStart = start;
        let rangeEnd = end;
        if (selectedRange) {
            const width = Math.min(selectedRange.end - selectedRange.start, end - start);
            rangeStart = selectedRange.followLatest ? end - width : Math.max(start, Math.min(selectedRange.start, end - width));
            rangeEnd = rangeStart + width;
        }
        drawing = true;
        rangeControl.setState({range: {start: new Date(rangeStart), end: new Date(rangeEnd)}});
        dashboard.draw(dataTable);
    }

    function candleTable(candles) {
        const table = new google.visualization.DataTable();
        table.addColumn('datetime', '時刻（UTC）');
        table.addColumn('number', '安値');
        table.addColumn('number', '始値');
        table.addColumn('number', '終値');
        table.addColumn('number', '高値');
        table.addColumn('number', '出来高');
        const rows = candles.map(candle => {
            const date = new Date(candle.time);
            const values = [candle.low, candle.open, candle.close, candle.high, candle.volume];
            if (!Number.isFinite(date.getTime()) || !values.every(Number.isFinite)) {
                throw new Error('invalid candle data');
            }
            return [date, ...values];
        });
        table.addRows(rows);
        new google.visualization.DateFormat({pattern: 'yyyy-MM-dd HH:mm:ss', timeZone: 0}).format(table, 0);
        return table;
    }

    async function refresh() {
        clearTimeout(timer);
        if (stopped || paused()) return;
        cancelRequest();
        const id = requestID;
        const current = new AbortController();
        controller = current;
        const timeout = setTimeout(() => current.abort(), requestTimeout);
        const params = new URLSearchParams({
            product_code: app.dataset.productCode, duration, limit: app.dataset.limit
        });
        try {
            const response = await fetch(`/api/candle/?${params}`, {signal: current.signal, cache: 'no-store'});
            if (!response.ok) throw new Error(`HTTP ${response.status}`);
            const data = await response.json();
            if (id !== requestID || stopped) return;
            if (!Array.isArray(data.candles)) throw new Error('invalid candle response');
            const table = candleTable(data.candles);
            dataTable = table;
            if (table.getNumberOfRows() === 0) {
                container.hidden = true;
                selectedRange = null;
                message('データを収集中です。データが届くと自動で表示します。');
            } else {
                container.hidden = false;
                draw();
                message(`${data.candles.length}本のローソク足を表示しています。`);
            }
        } catch (error) {
            if (id === requestID && !stopped) {
                message('データの取得に失敗しました。3秒後に再試行します。', true);
            }
        } finally {
            clearTimeout(timeout);
            if (id === requestID) {
                controller = null;
                schedule();
            }
        }
    }

    function interactionChanged() {
        if (paused()) {
            cancelRequest();
            message('チャートの操作中は自動更新を一時停止しています。');
        } else if (dashboard && !stopped) {
            refresh();
        }
    }

    for (const button of buttons) {
        button.addEventListener('click', () => {
            if (button.dataset.duration === duration) return;
            duration = button.dataset.duration;
            cancelRequest();
            dataTable = null;
            bounds = null;
            selectedRange = null;
            container.hidden = true;
            for (const item of buttons) item.setAttribute('aria-pressed', String(item.dataset.duration === duration));
            message('ローソク足データを読み込んでいます。');
            if (dashboard) refresh();
        });
    }
    container.addEventListener('mouseenter', () => { hovering = true; interactionChanged(); });
    container.addEventListener('mouseleave', () => { hovering = false; interactionChanged(); });
    container.addEventListener('focusin', () => { focused = true; interactionChanged(); });
    container.addEventListener('focusout', event => {
        focused = container.contains(event.relatedTarget);
        interactionChanged();
    });
    document.addEventListener('visibilitychange', interactionChanged);
    window.addEventListener('pagehide', () => { stopped = true; cancelRequest(); });
    window.addEventListener('pageshow', event => {
        if (event.persisted) {
            stopped = false;
            if (dashboard) refresh();
        }
    });
    let resizeTimer;
    window.addEventListener('resize', () => {
        clearTimeout(resizeTimer);
        resizeTimer = setTimeout(draw, 150);
    });

    if (typeof google === 'undefined') {
        message('チャートを読み込めませんでした。インターネット接続を確認して再読み込みしてください。', true);
        return;
    }
    google.charts.load('current', {packages: ['corechart', 'controls'], language: 'ja'});
    google.charts.setOnLoadCallback(() => {
        dashboard = new google.visualization.Dashboard(container);
        const mainChart = new google.visualization.ChartWrapper({
            chartType: 'ComboChart', containerId: 'chart_div',
            options: {
                height: 440, legend: 'none', seriesType: 'candlesticks',
                hAxis: {slantedText: false},
                candlestick: {
                    fallingColor: {strokeWidth: 0, fill: '#a52714'},
                    risingColor: {strokeWidth: 0, fill: '#0f9d58'}
                }
            },
            view: {columns: [{calc: (table, row) => table.getFormattedValue(row, 0), type: 'string'}, 1, 2, 3, 4]}
        });
        rangeControl = new google.visualization.ControlWrapper({
            controlType: 'ChartRangeFilter', containerId: 'filter_div',
            options: {
                filterColumnIndex: 0,
                ui: {chartType: 'LineChart', chartView: {columns: [0, 3]}, chartOptions: {height: 90, hAxis: {textPosition: 'none'}}}
            }
        });
        google.visualization.events.addListener(rangeControl, 'statechange', rememberRange);
        google.visualization.events.addListener(dashboard, 'ready', () => { drawing = false; });
        google.visualization.events.addListener(dashboard, 'error', () => {
            drawing = false;
            message('チャートを描画できませんでした。次の更新で再試行します。', true);
        });
        dashboard.bind(rangeControl, mainChart);
        refresh();
    });
})();
