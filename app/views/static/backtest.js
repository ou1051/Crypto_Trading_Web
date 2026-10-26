window.CandleBacktests = (() => {
    'use strict';
    const names = {ema: 'EMA', bbands: 'ボリンジャーバンド', ichimoku: '一目均衡表', macd: 'MACD', rsi: 'RSI', saved: '保存済みイベント', auto: '自動売買の記録'};
    const fields = {ema: ['emaPeriod1','emaPeriod2'], bbands: ['bbandsN','bbandsK'], ichimoku: [], macd: ['macdPeriod1','macdPeriod2','macdPeriod3'], rsi: ['rsiPeriod'], saved: []};
    const parameterLabels = {emaPeriod1:'短期',emaPeriod2:'長期',bbandsN:'期間',bbandsK:'倍率',macdPeriod1:'短期',macdPeriod2:'長期',macdPeriod3:'シグナル',rsiPeriod:'期間',rsiBuy:'買い',rsiSell:'売り'};
    const app = document.getElementById('chart_app');
    const form = document.getElementById('technical_form');
    const enabled = document.getElementById('inputEvents');
    const strategy = document.getElementById('backtest_strategy');
    const optimizeButton = document.getElementById('optimize_button');
    const profit = document.getElementById('backtest_profit');
    const ranking = document.getElementById('ranking_section');
    const rows = document.getElementById('ranking_rows');
    const caption = document.getElementById('ranking_caption');
    const buy = document.getElementById('rsi_buy');
    const sell = document.getElementById('rsi_sell');
    const format = new Intl.NumberFormat('ja-JP', {maximumFractionDigits: 2});
    let changed;
    strategy.value = app.dataset.defaultStrategy || 'ema';

    function activateIndicator() {
        for (const toggle of form.querySelectorAll('input[data-indicator]')) {
            if (toggle.dataset.indicator === strategy.value) toggle.checked = true;
        }
    }

    function initialize(onChange, onOptimize) {
        changed = onChange;
        enabled.addEventListener('change', () => { if (enabled.checked) activateIndicator(); changed(); });
        strategy.addEventListener('change', () => { if (enabled.checked) activateIndicator(); changed(); });
        buy.addEventListener('change', changed);
        sell.addEventListener('change', changed);
        optimizeButton.addEventListener('click', () => { enabled.checked = true; onOptimize(); });
    }

    function parameters() {
        const params = new URLSearchParams();
        if (!enabled.checked) return params;
        params.set('events', 'true');
        params.set('strategy', strategy.value);
        const inputs = [...form.querySelectorAll('input[data-for]')];
        for (const name of fields[strategy.value] || []) {
            const input = inputs.find(input => input.name === name);
            if (input) params.set(name, input.value);
        }
        if (strategy.value === 'ema' && Number(params.get('emaPeriod1')) >= Number(params.get('emaPeriod2'))) throw new Error('EMAの短期期間は長期期間より小さくしてください。');
        if (strategy.value === 'rsi') {
            if (!(Number(buy.value) > 0 && Number(buy.value) < Number(sell.value) && Number(sell.value) < 100)) throw new Error('RSIのしきい値は0より大きく100未満で、買いを売りより小さくしてください。');
            params.set('rsiBuy', buy.value);
            params.set('rsiSell', sell.value);
        }
        return params;
    }

    function money(value) { return `${format.format(value)} ${app.dataset.productCode.split('_').at(-1)}`; }

    function render(data) {
        const status = document.getElementById('automation_status');
        const position = document.getElementById('automation_position');
        const strategyStatus = document.getElementById('automation_strategy');
        const state = data.automation;
        status.dataset.error = String(Boolean(state?.halted || state?.last_error));
        if (state) {
            const mode = state.mode === 'paper' ? 'ペーパートレード（仮想売買）' : '実取引';
            const duration = `${state.duration / 1e9}秒足`;
            status.textContent = `${mode} ／ ${duration} ／ ${state.halted ? '停止中' : state.pending ? '注文の約定確認待ち' : '稼働中'}${state.last_error ? ` ／ ${state.last_error}` : ''}`;
            const cash = state.mode === 'paper' ? ` ／ 仮想残高 ${money(state.cash)}` : '';
            position.textContent = `保有 ${Number(state.quantity).toFixed(8)} BTC ／ 確定損益 ${money(state.realized_profit)}${cash} ／ 損切り価格 ${state.stop_limit > 0 ? money(state.stop_limit) : '—'}`;
            const params = state.params || {};
            const selected = [['ema_enable','EMA'],['bb_enable','ボリンジャーバンド'],['ichimoku_enable','一目均衡表'],['macd_enable','MACD'],['rsi_enable','RSI']].filter(([key]) => params[key]).map(([,name]) => name);
            strategyStatus.textContent = `採用指標：${selected.join('・') || 'データまたは次の最適化を待っています'}。${state.mode === 'paper' ? '仮想売買の手数料率' : '売却数量の手数料確保率'} ${(state.fee_rate * 100).toFixed(3)}%。`;
        } else {
            status.textContent = '自動売買の開始を待っています。';
            position.textContent = '';
            strategyStatus.textContent = '';
        }
        profit.hidden = !data.events;
        if (data.events) {
            const title = names[data.backtest?.strategy] || '売買イベント';
            const shortage = data.backtest?.available === false ? '／計算に必要な本数が不足しています' : '';
            const countLabel = data.backtest?.strategy === 'auto' ? '売却約定' : '完了取引';
            profit.textContent = `${title}：確定損益 ${money(data.events.profit)} ／ ${countLabel} ${data.events.completed_trades}回${data.events.holding ? ' ／ 未決済あり' : ''}${shortage}`;
        }
        if (!data.optimization) return;
        const result = data.optimization;
        ranking.hidden = false;
        rows.replaceChildren();
        const asOf = result.candle_count ? new Date(result.as_of).toISOString().replace('T',' ').slice(0,19) + ' UTC' : 'データなし';
        caption.textContent = `比較時点：${asOf} ／ ${result.candle_count}本。この区間内の比較で、正の確定損益を持つ上位戦略を採用します。`;
        for (const rank of result.rankings) {
            const row = document.createElement('tr');
            const parameters = Object.entries(rank.parameters).map(([key,value]) => `${parameterLabels[key] || key} ${value}`).join(' / ') || '9・26・52（固定）';
            for (const value of [names[rank.strategy], rank.available ? money(rank.performance) : '完了取引なし', rank.completed_trades, parameters, rank.enable ? '採用' : '—']) {
                const cell = document.createElement('td');
                cell.textContent = String(value);
                row.append(cell);
            }
            const cell = document.createElement('td');
            const button = document.createElement('button');
            button.type = 'button';
            button.textContent = '設定を表示';
            button.disabled = !rank.available;
            button.addEventListener('click', () => {
                enabled.checked = true;
                strategy.value = rank.strategy;
                activateIndicator();
                for (const input of form.querySelectorAll('input[data-for]')) {
                    if (rank.parameters[input.name] != null) input.value = String(rank.parameters[input.name]);
                }
                if (rank.parameters.rsiBuy != null) buy.value = String(rank.parameters.rsiBuy);
                if (rank.parameters.rsiSell != null) sell.value = String(rank.parameters.rsiSell);
                changed();
            });
            cell.append(button);
            row.append(cell);
            rows.append(row);
        }
    }

    function busy(value) { optimizeButton.disabled = value; optimizeButton.textContent = value ? '最適化しています…' : '戦略を比較・最適化'; }
    function reset() { ranking.hidden = true; profit.hidden = true; }
    return {initialize, parameters, render, busy, reset};
})();
