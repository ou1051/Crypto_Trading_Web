window.CandleBacktests = (() => {
    'use strict';
    const names = {ema: 'EMA', bbands: 'BBands', ichimoku: 'Ichimoku', macd: 'MACD', rsi: 'RSI', saved: 'Saved events', auto: 'Auto trades'};
    const fields = {ema: ['emaPeriod1','emaPeriod2'], bbands: ['bbandsN','bbandsK'], ichimoku: [], macd: ['macdPeriod1','macdPeriod2','macdPeriod3'], rsi: ['rsiPeriod'], saved: []};
    const parameterLabels = {emaPeriod1:'short',emaPeriod2:'long',bbandsN:'N',bbandsK:'K',macdPeriod1:'fast',macdPeriod2:'slow',macdPeriod3:'signal',rsiPeriod:'period',rsiBuy:'buy',rsiSell:'sell'};
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
    const format = new Intl.NumberFormat('en-US', {maximumFractionDigits: 2});
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
        if (strategy.value === 'ema' && Number(params.get('emaPeriod1')) >= Number(params.get('emaPeriod2'))) throw new Error('EMA short period must be less than its long period.');
        if (strategy.value === 'rsi') {
            if (!(Number(buy.value) > 0 && Number(buy.value) < Number(sell.value) && Number(sell.value) < 100)) throw new Error('RSI levels must satisfy 0 < buy < sell < 100.');
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
            const mode = state.mode === 'paper' ? 'Paper trading' : 'Live trading';
            const duration = {1000000000:'1s',60000000000:'1m',3600000000000:'1h'}[state.duration] || `${state.duration / 1e9}s`;
            status.textContent = `${mode} | ${duration} | ${state.halted ? 'Stopped' : state.pending ? 'Waiting for fill' : 'Running'}${state.last_error ? ` | ${state.last_error}` : ''}`;
            const cash = state.mode === 'paper' ? ` | Paper cash ${money(state.cash)}` : '';
            position.textContent = `Position ${Number(state.quantity).toFixed(8)} BTC | Realized profit ${money(state.realized_profit)}${cash} | Stop ${state.stop_limit > 0 ? money(state.stop_limit) : '-'}`;
            const params = state.params || {};
            const selected = [['ema_enable','EMA'],['bb_enable','BBands'],['ichimoku_enable','Ichimoku'],['macd_enable','MACD'],['rsi_enable','RSI']].filter(([key]) => params[key]).map(([,name]) => name);
            const slip = state.policy?.costs?.slippage_rate || 0;
            strategyStatus.textContent = `Indicators: ${selected.join(', ') || 'waiting for data or optimization'}. ${state.mode === 'paper' ? 'Paper fee' : 'Sale fee reserve'} ${(state.fee_rate * 100).toFixed(3)}%.${state.mode === 'paper' ? ` Paper slippage ${(slip * 100).toFixed(3)}% per fill.` : ''}`;
        } else {
            status.textContent = 'Trading has not started.';
            position.textContent = '';
            strategyStatus.textContent = '';
        }
        profit.hidden = !data.events;
        if (data.events) {
            const title = names[data.backtest?.strategy] || 'Events';
            const shortage = data.backtest?.available === false ? (data.backtest.strategy === 'auto' ? ' | Trading has not started' : ' | Not enough continuous candle data') : '';
            const countLabel = data.backtest?.strategy === 'auto' ? 'Sell fills' : 'Trades';
            profit.textContent = `${title}: Realized profit ${money(data.events.profit)} | ${countLabel} ${data.events.completed_trades}${data.events.holding ? ' | Open position' : ''}${shortage}`;
            if (data.backtest?.costs) profit.textContent += ` | Gross ${money(data.backtest.gross_profit)}`;
        }
        if (!data.optimization) return;
        const result = data.optimization;
        ranking.hidden = false;
        rows.replaceChildren();
        const asOf = result.candle_count ? new Date(result.as_of).toISOString().replace('T',' ').slice(0,19) + ' UTC' : 'no data';
        if (result.policy) {
            const costs = result.policy.costs;
            caption.textContent = `As of ${asOf}. Training: ${result.training_count} bars; validation: ${result.validation_count} bars, starting flat. Selection needs positive net profit in both periods and at least ${result.policy.min_validation_trades} validation trades. Fee ${(costs.fee_rate*100).toFixed(3)}% per fill, full spread ${(costs.spread_rate*100).toFixed(3)}%, slippage ${(costs.slippage_rate*100).toFixed(3)}% per fill. Modeled round-trip break-even: ${(result.break_even_rise*100).toFixed(3)}%. Validation also influences selection.`;
        } else {
            caption.textContent = `As of ${asOf}. ${result.candle_count} bars.`;
        }
        for (const rank of result.rankings) {
            const row = document.createElement('tr');
            const parameters = Object.entries(rank.parameters).map(([key,value]) => `${parameterLabels[key] || key} ${value}`).join(' / ') || '9/26/52 (fixed)';
            for (const value of [names[rank.strategy], rank.training_profit == null ? '-' : money(rank.training_profit), rank.available ? money(rank.gross_performance ?? rank.performance) : 'No trades', rank.available ? money(rank.performance) : 'No trades', rank.completed_trades, parameters, rank.enable ? 'Yes' : 'No']) {
                const cell = document.createElement('td');
                cell.textContent = String(value);
                row.append(cell);
            }
            const cell = document.createElement('td');
            const button = document.createElement('button');
            button.type = 'button';
            button.textContent = 'Show';
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

    function busy(value) { optimizeButton.disabled = value; optimizeButton.textContent = value ? 'Optimizing...' : 'Optimize'; }
    function reset() { ranking.hidden = true; profit.hidden = true; }
    return {initialize, parameters, render, busy, reset};
})();
