const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../app/views/static/chart.js'), 'utf8');
const flush = () => new Promise(resolve => setImmediate(resolve));

class Element {
    constructor(dataset = {}) {
        this.dataset = dataset;
        this.listeners = new Map();
        this.attributes = {};
        this.hidden = false;
        this.textContent = '';
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
}

function application({withGoogle = true, respectAbort = false} = {}) {
    const buttons = ['1s', '1m', '1h'].map(duration => new Element({duration}));
    const root = new Element({productCode: 'BTC_JPY', duration: '1m', limit: '365'});
    root.querySelectorAll = () => buttons;
    const status = new Element();
    const container = new Element();
    container.hidden = true;
    const document = new Element();
    const elements = {chart_app: root, chart_status: status, dashboard_div: container};
    document.getElementById = id => elements[id];
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
        trigger(target, type) { target.listeners?.emit(type); }
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
                bind(control, chart) { observed.control = control; observed.chart = chart; }
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
    vm.runInNewContext(source, sandbox);
    return {
        root, status, container, document, window, requests, timers, draws, observed,
        change(duration) { buttons.find(button => button.dataset.duration === duration).emit('click'); },
        buttons,
        async respond(index, candles, statusCode = 200) {
            requests[index].resolve({ok: statusCode === 200, status: statusCode, json: async () => ({candles})});
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
