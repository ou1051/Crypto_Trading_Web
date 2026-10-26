const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../app/views/static/chart.js'), 'utf8');
const indicatorSource = fs.readFileSync(path.join(__dirname, '../app/views/static/indicators.js'), 'utf8');
const backtestSource = fs.readFileSync(path.join(__dirname, '../app/views/static/backtest.js'), 'utf8');
const flush = () => new Promise(resolve => setImmediate(resolve));

class Element {
    constructor(dataset = {}) {
        this.dataset = dataset;
        this.listeners = new Map();
        this.attributes = {};
        this.hidden = false;
        this.textContent = '';
        this.value = '';
        this.checked = false;
        this.children = [];
    }
    addEventListener(type, handler) {
        if (!this.listeners.has(type)) this.listeners.set(type, []);
        this.listeners.get(type).push(handler);
    }
    emit(type, event = {}) {
        for (const handler of this.listeners.get(type) || []) handler(event);
    }
    setAttribute(name, value) { this.attributes[name] = value; }
    contains(target) { return target === this; }
    append(...children) { this.children.push(...children); }
    replaceChildren(...children) { this.children = [...children]; }
}

function application({withGoogle = true, respectAbort = false} = {}) {
    const buttons = ['1s', '1m', '1h'].map(duration => new Element({duration}));
    const root = new Element({productCode: 'BTC_JPY', duration: '1m', limit: '365'});
    root.querySelectorAll = () => buttons;
    const status = new Element();
    const container = new Element();
    container.hidden = true;
    const document = new Element();
    const form = new Element();
    const toggles = ['sma', 'ema', 'bbands', 'ichimoku', 'volume', 'rsi', 'macd', 'hv'].map(indicator => {
        const input = new Element({indicator}); input.checked = false; return input;
    });
    const inputs = [];
    for (const [group, parameters] of Object.entries({sma: {smaPeriod1:7,smaPeriod2:14,smaPeriod3:50},ema:{emaPeriod1:7,emaPeriod2:14,emaPeriod3:50},bbands:{bbandsN:20,bbandsK:2},rsi:{rsiPeriod:14},macd:{macdPeriod1:12,macdPeriod2:26,macdPeriod3:9},hv:{hvPeriod1:21,hvPeriod2:63,hvPeriod3:252}})) {
        for (const [name,value] of Object.entries(parameters)) {
            const input = new Element({for:group});
            Object.assign(input, {name,value:String(value),disabled:true}); inputs.push(input);
        }
    }
    form.checkValidity = () => inputs.every(input => input.disabled || (Number(input.value) >= (input.name === 'bbandsK' ? .1 : 2) && Number(input.value) <= (input.name === 'bbandsK' ? 10 : 1000)));
    form.querySelectorAll = selector => {
        if (selector === 'input[data-indicator]') return toggles;
        if (selector === 'input[data-for]') return inputs;
        return inputs.filter(input => selector === `input[data-for="${input.dataset.for}"]`);
    };
    const elements = {chart_app: root, chart_status: status, dashboard_div: container, technical_form: form};
    document.getElementById = id => elements[id] ||= new Element();
    document.createElement = () => new Element();
    document.getElementById('rsi_buy').value = '30';
    document.getElementById('rsi_sell').value = '70';
    document.hidden = false;
    const window = new Element();
    const requests = [];
    const timers = new Map();
    const draws = [];
    const observed = {};
    let nextTimer = 0;
    const events = {
        addListener(target, type, handler) {
            if (!target.listeners) target.listeners = new Element();
            target.listeners.addEventListener(type, handler);
        },
        trigger(target, type) { target.listeners?.emit(type); },
        removeAllListeners(target) { target.listeners = new Element(); }
    };
    const sandbox = {
        document, window, AbortController, URLSearchParams, console,
        setTimeout(handler, delay) { timers.set(++nextTimer, {handler, delay}); return nextTimer; },
        clearTimeout(id) { timers.delete(id); },
        fetch(url, options) {
            return new Promise((resolve, reject) => {
                requests.push({url, options, resolve});
                if (respectAbort) options.signal.addEventListener('abort', () => reject(new Error('aborted')));
            });
        }
    };
    if (withGoogle) sandbox.google = {
        charts: {
            load(version, options) { observed.packages = options.packages; },
            setOnLoadCallback(callback) { callback(); }
        },
        visualization: {
            events,
            DataTable: class {
                constructor() { this.columns = []; this.rows = []; }
                addColumn(type, name) { this.columns.push({type, name}); }
                addRows(rows) { this.rows.push(...rows); }
                getNumberOfRows() { return this.rows.length; }
                getValue(row, column) { return this.rows[row][column]; }
            },
            DateFormat: class {
                constructor(options) { observed.dateFormat = options; }
                format() {}
            },
            Dashboard: class {
                constructor(element) { assert.equal(element, container); }
                bind(control, charts) { observed.control = control; observed.charts = charts; observed.chart = charts[0]; }
                draw(table) { draws.push(table); events.trigger(this, 'ready'); }
            },
            ChartWrapper: class { constructor(options) { this.options = options; } },
            ControlWrapper: class {
                constructor(options) { this.options = options; }
                setState(state) { this.state = state; }
                getState() { return this.state; }
            }
        }
    };
    vm.runInNewContext(indicatorSource + '\n' + backtestSource + '\n' + source, sandbox);
    return {
        root, status, container, document, window, requests, timers, draws, observed,
        change(duration) { buttons.find(button => button.dataset.duration === duration).emit('click'); },
        buttons,
        elements,
        showEvents(strategy='ema') {
            document.getElementById('backtest_strategy').value = strategy;
            document.getElementById('inputEvents').checked = true;
            document.getElementById('inputEvents').emit('change');
        },
        optimize() { document.getElementById('optimize_button').emit('click'); },
        enable(name, checked=true) { toggles.find(input => input.dataset.indicator === name).checked = checked; form.emit('change'); },
        parameter(name, value) { inputs.find(input => input.name === name).value = String(value); form.emit('change'); },
        async respond(index, candles, statusCode = 200, extra = {}) {
            requests[index].resolve({ok: statusCode === 200, status: statusCode, json: async () => ({...extra,candles})});
            await flush();
        },
        async tick(delay = 3000) {
            const timer = [...timers].find(([, timer]) => timer.delay === delay);
            assert.ok(timer, `no timer scheduled for ${delay}ms`);
            timers.delete(timer[0]);
            timer[1].handler();
            await flush();
        },
        selectRange(start, end) {
            observed.control.setState({range: {start: new Date(start), end: new Date(end)}});
            events.trigger(observed.control, 'statechange');
        }
    };
}

const minute = 60000;
const start = Date.parse('2026-10-25T10:00:00Z');
function candles(count = 3, price = 100) {
    return Array.from({length: count}, (_, i) => ({
        time: new Date(start + i * minute).toISOString(),
        low: price - 10, open: price, close: price + 5, high: price + 10, volume: 2
    }));
}

test('fetches candles and builds the ComboChart and range filter using the API column order', async () => {
    const app = application();
    const url = new URL(app.requests[0].url, 'http://localhost');
    assert.equal(url.searchParams.get('product_code'), 'BTC_JPY');
    assert.equal(url.searchParams.get('duration'), '1m');
    assert.equal(url.searchParams.get('limit'), '365');
    assert.equal(app.requests[0].options.cache, 'no-store');
    await app.respond(0, candles());
    assert.equal(app.observed.chart.options.chartType, 'ComboChart');
    assert.equal(app.observed.control.options.controlType, 'ChartRangeFilter');
    assert.equal(app.observed.dateFormat.timeZone, 0);
    assert.equal(app.draws[0].rows[0][0].getTime(), start);
    assert.deepEqual(Array.from(app.draws[0].rows[0].slice(1)), [90, 100, 105, 110, 2]);
    assert.equal(app.container.hidden, false);
    await app.tick();
    assert.equal(app.requests.length, 2);
});

test('duration switches cancel earlier requests and ignore their late responses', async () => {
    const app = application();
    app.change('1s');
    app.change('1h');
    assert.equal(app.requests.length, 3);
    assert.equal(app.requests[0].options.signal.aborted, true);
    assert.equal(app.requests[1].options.signal.aborted, true);
    assert.match(app.requests[2].url, /duration=1h/);
    await app.respond(2, candles(3, 200));
    await app.respond(0, candles(3, 999));
    await app.respond(1, candles(3, 888));
    assert.equal(app.draws.length, 1);
    assert.equal(app.draws[0].rows[0][2], 200);
    assert.equal(app.buttons[2].attributes['aria-pressed'], 'true');
});

test('empty data and HTTP failures recover on the next poll without reloading the page', async () => {
    const app = application();
    await app.respond(0, []);
    assert.equal(app.container.hidden, true);
    assert.match(app.status.textContent, /データを収集中/);
    await app.tick();
    await app.respond(1, [], 500);
    assert.equal(app.status.dataset.error, 'true');
    await app.tick();
    await app.respond(2, candles());
    assert.equal(app.container.hidden, false);
    assert.equal(app.status.dataset.error, 'false');
});

test('pauses polling and in-flight updates during interaction, then resumes', async () => {
    const app = application();
    await app.respond(0, candles());
    await app.tick();
    app.container.emit('mouseenter');
    assert.equal(app.requests[1].options.signal.aborted, true);
    await app.respond(1, candles(3, 999));
    assert.equal(app.draws.length, 1);
    assert.equal([...app.timers.values()].some(timer => timer.delay === 3000), false);
    app.container.emit('mouseleave');
    assert.equal(app.requests.length, 3);
    await app.respond(2, candles());
    assert.equal(app.draws.length, 2);
});

test('keeps a selected time range across refreshes and follows the newest candle when selected', async () => {
    const app = application();
    await app.respond(0, candles());
    app.selectRange(start, start + minute);
    await app.tick();
    await app.respond(1, candles(4));
    assert.equal(app.observed.control.getState().range.start.getTime(), start);
    assert.equal(app.observed.control.getState().range.end.getTime(), start + minute);
    app.selectRange(start + 2 * minute, start + 3 * minute);
    await app.tick();
    await app.respond(2, candles(5));
    assert.equal(app.observed.control.getState().range.start.getTime(), start + 3 * minute);
    assert.equal(app.observed.control.getState().range.end.getTime(), start + 4 * minute);
});

test('a request timeout retries and page navigation stops further polling', async () => {
    const app = application({respectAbort: true});
    await app.tick(15000);
    assert.equal(app.status.dataset.error, 'true');
    await app.tick();
    assert.equal(app.requests.length, 2);
    app.window.emit('pagehide');
    await flush();
    assert.equal(app.requests[1].options.signal.aborted, true);
    assert.equal([...app.timers.values()].some(timer => timer.delay === 3000), false);
});

test('reports a missing chart library and rejects malformed candle timestamps', async () => {
    const offline = application({withGoogle: false});
    assert.equal(offline.requests.length, 0);
    assert.equal(offline.status.dataset.error, 'true');
    const app = application();
    await app.respond(0, [{...candles(1)[0], time: 'invalid'}]);
    assert.equal(app.draws.length, 0);
    assert.equal(app.status.dataset.error, 'true');
});

test('indicator controls send selected periods and refuse invalid settings', async () => {
    const app = application();
    app.enable('sma');
    let params = new URL(app.requests.at(-1).url, 'http://localhost').searchParams;
    assert.equal(params.get('sma'), 'true');
    assert.equal(params.get('smaPeriod1'), '7');
    app.parameter('smaPeriod1', 9);
    params = new URL(app.requests.at(-1).url, 'http://localhost').searchParams;
    assert.equal(params.get('smaPeriod1'), '9');
    const count = app.requests.length;
    app.parameter('smaPeriod1', 0);
    assert.equal(app.requests.length, count);
    assert.equal(app.status.dataset.error, 'true');
    app.enable('sma', false);
    assert.equal(app.requests.length, count + 1, 'disabled invalid fields must not prevent polling');
    app.enable('macd');
    const before = app.requests.length;
    app.parameter('macdPeriod1', 30);
    assert.equal(app.requests.length, before);
    assert.match(app.status.textContent, /短期期間/);
});

test('all indicators build linked charts, retain null warmups and project Ichimoku 26 bars', async () => {
    const app = application();
    for (const name of ['sma','ema','bbands','ichimoku','volume','rsi','macd','hv']) app.enable(name);
    const n = 80;
    const values = Array.from({length:n},(_,i)=>i < 7 ? null : 100);
    const extra = {
        smas:[{period:7,values}], emas:[{period:7,values}],
        bbands:{n:20,k:2,up:values,mid:values,down:values},
        ichimoku:{tenkan:values,kijun:values,senkoua:[...values,...Array(26).fill(110)],senkoub:[...values,...Array(26).fill(90)],chikou:values,displacement:26},
        rsi:{period:14,values},macd:{fast_period:12,slow_period:26,signal_period:9,macd:values,macd_signal:values,macd_hist:values},hvs:[{period:21,values}]
    };
    await app.respond(app.requests.length-1,candles(n),200,extra);
    assert.equal(app.observed.charts.length,5);
    assert.equal(app.draws.at(-1).getNumberOfRows(),n+26);
    assert.equal(app.draws.at(-1).rows[0][6],null,'warmup must remain null');
    assert.equal(app.draws.at(-1).rows[n][1],null,'projected dates must not invent candle prices');
    assert.equal(app.draws.at(-1).rows.at(-1)[0].getTime(), start+(n-1+26)*minute);
    assert.equal(app.observed.chart.options.options.series[1].type,'line');
    app.enable('volume',false);
    app.enable('rsi',false);
    delete extra.rsi;
    await app.respond(app.requests.length-1,candles(n),200,extra);
    assert.equal(app.observed.charts.length,3,'disabled subcharts must be removed');
});

test('backtest settings request the chosen strategy and render event markers without mutating signals', async () => {
    const app = application();
    app.showEvents('rsi');
    const params = new URL(app.requests.at(-1).url,'http://localhost').searchParams;
    assert.equal(params.get('events'),'true');
    assert.equal(params.get('strategy'),'rsi');
    assert.equal(params.get('rsiPeriod'),'14');
    assert.equal(params.get('rsiBuy'),'30');
    const signals = [{time:new Date(start-minute).toISOString(),side:'BUY',price:50,size:1},{time:new Date(start+minute).toISOString(),side:'SELL',price:105,size:1}];
    const events = {signals,profit:55,completed_trades:1,holding:false};
    await app.respond(app.requests.length-1,candles(3),200,{events,backtest:{strategy:'rsi',available:true}});
    assert.equal(signals.length,2,'rendering must not consume the event list');
    const rows = app.draws.at(-1).rows;
    assert.equal(rows[1].at(-3),105);
    assert.equal(rows[1].at(-2),'SELL');
    assert.equal(rows[0].at(-2),null,'an event outside the visible window must not block later markers');
    assert.match(app.elements.backtest_profit.textContent,/55 JPY/);
});

test('optimization runs only once and its parameters can be applied from the ranking', async () => {
    const app = application();
    app.optimize();
    assert.equal(new URL(app.requests.at(-1).url,'http://localhost').searchParams.get('optimize'),'true');
    const events = {signals:[],profit:0,completed_trades:0,holding:false};
    const optimization = {as_of:new Date(start).toISOString(),candle_count:100,rankings:[{strategy:'ema',available:true,performance:10,completed_trades:2,parameters:{emaPeriod1:5,emaPeriod2:12},enable:true}]};
    await app.respond(app.requests.length-1,candles(),200,{events,backtest:{strategy:'ema',available:true},optimization});
    assert.equal(app.elements.ranking_rows.children.length,1);
    assert.match(app.elements.backtest_profit.textContent,/0 JPY/,'zero profit must be visible');
    await app.tick();
    assert.equal(new URL(app.requests.at(-1).url,'http://localhost').searchParams.has('optimize'),false,'periodic polling must not repeat optimization');
    const apply = app.elements.ranking_rows.children[0].children[5].children[0];
    apply.emit('click');
    const params = new URL(app.requests.at(-1).url,'http://localhost').searchParams;
    assert.equal(params.get('strategy'),'ema');
    assert.equal(params.get('emaPeriod1'),'5');
    assert.equal(params.get('emaPeriod2'),'12');
});

test('automation status distinguishes paper trades from live orders and shows halted state', async () => {
    const app = application();
    const automation = {mode:'paper',duration:60000000000,cash:1000000,quantity:.01,realized_profit:1234,stop_limit:9000000,fee_rate:.0012,params:{ema_enable:true},halted:false,last_error:''};
    await app.respond(0,candles(),200,{automation});
    assert.match(app.elements.automation_status.textContent,/ペーパートレード.*60秒足.*稼働中/);
    assert.match(app.elements.automation_position.textContent,/0\.01000000 BTC.*1,234 JPY.*仮想残高/);
    assert.match(app.elements.automation_strategy.textContent,/EMA.*0\.120%/);
    await app.tick();
    await app.respond(app.requests.length-1,candles(),200,{automation:{...automation,mode:'live',halted:true,pending:{acceptance_id:'JRF-owned'},last_error:'Order outcome unknown'}});
    assert.match(app.elements.automation_status.textContent,/実取引.*停止中.*Order outcome unknown/);
    assert.equal(app.elements.automation_status.dataset.error,'true');
    assert.doesNotMatch(app.elements.automation_position.textContent,/仮想残高/);
});

test('automatic trade markers use execution prices and group multiple fills in the displayed candle', async () => {
    const app = application();
    app.showEvents('auto');
    assert.equal(new URL(app.requests.at(-1).url,'http://localhost').searchParams.get('strategy'),'auto');
    const signals = [{time:new Date(start+1000).toISOString(),side:'BUY',size:.01,price:102,execution_price:100},
        {time:new Date(start+30000).toISOString(),side:'SELL',size:.01,price:108,execution_price:110}];
    await app.respond(app.requests.length-1,candles(),200,{events:{signals,profit:.06,completed_trades:1,holding:false},backtest:{strategy:'auto',available:true}});
    const row=app.draws.at(-1).rows[0];
    assert.equal(row.at(-3),110);
    assert.equal(row.at(-2),'BUY/SELL');
    assert.equal(row.at(-1),'BUY 0.01 BTC @ 100\nSELL 0.01 BTC @ 110');
    assert.match(app.elements.backtest_profit.textContent,/自動売買の記録.*売却約定/);
    assert.equal(signals.length,2);
});
