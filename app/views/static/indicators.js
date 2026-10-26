window.CandleIndicators = (() => {
    'use strict';

    function settings(form) {
        if (!form.checkValidity()) throw new Error('指標の期間や倍率を入力範囲内で指定してください。');
        const params = new URLSearchParams();
        const enabled = {};
        for (const input of form.querySelectorAll('input[data-indicator]')) {
            enabled[input.dataset.indicator] = input.checked;
            if (input.checked && input.dataset.indicator !== 'volume') params.set(input.dataset.indicator, 'true');
        }
        for (const input of form.querySelectorAll('input[data-for]')) {
            if (enabled[input.dataset.for]) params.set(input.name, input.value);
        }
        if (enabled.macd && Number(params.get('macdPeriod1')) >= Number(params.get('macdPeriod2'))) {
            throw new Error('MACDの短期期間は長期期間より小さくしてください。');
        }
        return {params, enabled};
    }

    function layout(data, selected, duration) {
        const table = new google.visualization.DataTable();
        const labels = ['時刻（UTC）', '安値', '始値', '終値', '高値', '出来高'];
        labels.forEach((label, i) => table.addColumn(i === 0 ? 'datetime' : 'number', label));
        const vectors = [];
        const overlays = [];
        const panels = [];
        function column(label, values) {
            if (!Array.isArray(values)) throw new Error('invalid indicator values');
            const index = labels.length;
            labels.push(label);
            table.addColumn('number', label);
            vectors.push(values);
            return index;
        }
        function overlay(label, values, color) {
            overlays.push({index: column(label, values), color});
        }
        const colors = ['#1565c0', '#e65100', '#6a1b9a'];
        for (const [key, name, palette] of [['smas', 'SMA', colors], ['emas', 'EMA', ['#00838f', '#ad1457', '#558b2f']]]) {
            (data[key] || []).forEach((line, i) => overlay(`${name} ${line.period}`, line.values, palette[i % palette.length]));
        }
        if (data.bbands) {
            const bands = data.bbands;
            overlay(`BB ${bands.n} +${bands.k}σ`, bands.up, '#455a64');
            overlay(`BB ${bands.n} 中央`, bands.mid, '#78909c');
            overlay(`BB ${bands.n} −${bands.k}σ`, bands.down, '#455a64');
        }
        if (data.ichimoku) {
            const cloud = data.ichimoku;
            overlay('転換線', cloud.tenkan, '#1565c0');
            overlay('基準線', cloud.kijun, '#c62828');
            overlay('先行スパンA', cloud.senkoua, '#2e7d32');
            overlay('先行スパンB', cloud.senkoub, '#ef6c00');
            overlay('遅行スパン', cloud.chikou, '#6a1b9a');
        }
        if (selected.enabled.volume) panels.push({name: 'volume', type: 'ColumnChart', columns: [5], options: {colors: ['#78909c'], vAxis: {minValue: 0}}});
        if (data.rsi) {
            const thresholds = value => data.candles.map(() => value);
            panels.push({name: 'rsi', type: 'LineChart', columns: [column('上限 70', thresholds(70)), column(`RSI ${data.rsi.period}`, data.rsi.values), column('下限 30', thresholds(30))], options: {
                vAxis: {viewWindow: {min: 0, max: 100}}, series: {0: {color: '#90a4ae', lineDashStyle: [4, 4]}, 1: {color: '#ad1457'}, 2: {color: '#90a4ae', lineDashStyle: [4, 4]}}
            }});
        }
        if (data.macd) {
            const macd = data.macd;
            panels.push({name: 'macd', type: 'ComboChart', columns: [column('ヒストグラム', macd.macd_hist), column(`MACD ${macd.fast_period}/${macd.slow_period}`, macd.macd), column(`シグナル ${macd.signal_period}`, macd.macd_signal)], options: {
                seriesType: 'bars', series: {0: {color: '#b0bec5'}, 1: {type: 'line', color: '#1565c0'}, 2: {type: 'line', color: '#e65100'}}
            }});
        }
        if (data.hvs?.length) panels.push({name: 'hv', type: 'LineChart', columns: data.hvs.map(line => column(`HV ${line.period}`, line.values)), options: {colors, vAxis: {minValue: 0}}});

        const future = data.ichimoku ? data.ichimoku.displacement : 0;
        if (!Number.isInteger(future) || future < 0 || future > 26) throw new Error('invalid Ichimoku displacement');
        const step = {'1s': 1000, '1m': 60000, '1h': 3600000}[duration];
        const lastTime = data.candles.length ? new Date(data.candles.at(-1).time).getTime() : 0;
        function nullable(value) {
            if (value == null) return null;
            if (!Number.isFinite(value)) throw new Error('invalid indicator value');
            return value;
        }
        const rows = [];
        for (let i = 0; i < data.candles.length + future; i++) {
            const candle = data.candles[i];
            const date = candle ? new Date(candle.time) : new Date(lastTime + (i - data.candles.length + 1) * step);
            const values = candle ? [candle.low, candle.open, candle.close, candle.high, candle.volume] : [null, null, null, null, null];
            if (!Number.isFinite(date.getTime()) || (candle && !values.every(Number.isFinite))) throw new Error('invalid candle data');
            rows.push([date, ...values, ...vectors.map(values => nullable(values[i]))]);
        }
        table.addRows(rows);
        new google.visualization.DateFormat({pattern: 'yyyy-MM-dd HH:mm:ss', timeZone: 0}).format(table, 0);
        return {table, overlays, panels, signature: JSON.stringify({labels, panels: panels.map(panel => panel.name)})};
    }

    function charts(layout) {
        const dateColumn = {calc: (table, row) => table.getFormattedValue(row, 0), type: 'string'};
        const series = {0: {labelInLegend: 'ローソク足'}};
        layout.overlays.forEach((line, i) => { series[i + 1] = {type: 'line', color: line.color, lineWidth: 1}; });
        const charts = [new google.visualization.ChartWrapper({
            chartType: 'ComboChart', containerId: 'chart_div',
            options: {
                height: 440, legend: layout.overlays.length ? {position: 'bottom', maxLines: 4, textStyle: {fontSize: 11}} : 'none',
                seriesType: 'candlesticks', series, interpolateNulls: false, hAxis: {slantedText: false},
                candlestick: {fallingColor: {strokeWidth: 0, fill: '#a52714'}, risingColor: {strokeWidth: 0, fill: '#0f9d58'}}
            },
            view: {columns: [dateColumn, 1, 2, 3, 4, ...layout.overlays.map(line => line.index)]}
        })];
        for (const name of ['volume', 'rsi', 'macd', 'hv']) document.getElementById(`${name}_panel`).hidden = !layout.panels.some(panel => panel.name === name);
        for (const panel of layout.panels) charts.push(new google.visualization.ChartWrapper({
            chartType: panel.type, containerId: `${panel.name}_chart`,
            options: {height: 170, legend: {position: 'bottom', textStyle: {fontSize: 11}}, hAxis: {slantedText: false}, interpolateNulls: false, ...panel.options},
            view: {columns: [dateColumn, ...panel.columns]}
        }));
        return charts;
    }

    return {settings, layout, charts};
})();
