// A page reader for the TECHO5 voice assistant: loads a page in headless Chromium, scripts and all,
// and returns the HTML it ends up with, for pages that are empty until their scripts run.
// GET /read?url=https://...  ->  200 text/html (X-Final-URL: where it ended up), or 4xx/5xx text.
// The container's firewall (entrypoint.sh) keeps the browser off private and 100.64.0.0/10 addresses,
// and off any further ranges set in READER_BLOCK.
const http = require('http');
const { chromium } = require('playwright');

const PORT = 8891;
const MAX_HTML = 3 << 20;   // what echod reads of a page at most
const NAV_TIMEOUT = 15000;  // loading the page
const SETTLE = 4000;        // waiting for its scripts to fill it in
const MAX_BUSY = 2;         // pages at once; more get 503
const DEADLINE = 25000;     // the whole read, whatever the page does; then it is torn down
const CLOSE_WAIT = 5000;    // closing a page's context before the whole browser goes instead
const UA = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36';

let launching = null; // the browser, as one launch every request shares
let busy = 0;

function startBrowser() {
	const p = chromium.launch().then((b) => {
		p.ready = true;
		b.on('disconnected', () => {
			if (launching === p) launching = null;
		});
		return b;
	});
	launching = p;
	p.catch(() => {
		if (launching === p) launching = null;
	});
	return p;
}

// getBrowser returns the running browser, or starts one; requests that come in while it starts wait
// for that same launch instead of starting a second Chromium.
async function getBrowser() {
	const p = launching || startBrowser();
	const b = await p;
	if (b.isConnected()) return b;
	if (launching === p) startBrowser();
	return launching || startBrowser();
}

// within resolves with p's result, or rejects once ms have passed.
function within(p, ms, what) {
	let timer;
	const t = new Promise((_, reject) => {
		timer = setTimeout(() => reject(new Error(what)), Math.max(ms, 0));
	});
	return Promise.race([p, t]).finally(() => clearTimeout(timer));
}

// dropBrowser closes the browser, or kills it if it will not close, so the next read starts a new one.
async function dropBrowser() {
	const p = launching;
	launching = null;
	if (!p) return;
	const b = await p.catch(() => null);
	if (!b) return;
	try {
		await within(b.close(), CLOSE_WAIT, 'browser close timed out');
	} catch {
		const proc = b.process();
		if (proc) proc.kill('SIGKILL');
	}
}

// read loads target under one hard deadline. A page that keeps its main thread busy can hold
// page.content() forever, so past the deadline its context is closed, and if even that hangs the
// whole browser goes and the next read launches a new one.
async function read(target) {
	const end = Date.now() + DEADLINE;
	const opening = getBrowser().then((b) => b.newContext({ userAgent: UA, serviceWorkers: 'block', locale: 'en-US' }));
	let ctx;
	try {
		ctx = await within(opening, DEADLINE, 'browser did not start');
	} catch (e) {
		opening.then((c) => c.close()).catch(() => {});
		// A launch or a new context that hangs is given up on, so the next read starts afresh.
		const stuck = launching;
		if (stuck && !stuck.ready) {
			launching = null;
			stuck.then((b) => b.close()).catch(() => {});
		} else {
			await dropBrowser();
		}
		throw e;
	}
	try {
		return await within(load(ctx, target), end - Date.now(), 'page took too long');
	} finally {
		try {
			await within(ctx.close(), CLOSE_WAIT, 'page close timed out');
		} catch {
			await dropBrowser();
		}
	}
}

async function load(ctx, target) {
	const page = await ctx.newPage();
	await page.route('**/*', (route) => {
		const req = route.request();
		const proto = new URL(req.url()).protocol;
		if ((proto !== 'http:' && proto !== 'https:') || ['image', 'media', 'font'].includes(req.resourceType())) {
			return route.abort();
		}
		return route.continue();
	});
	const resp = await page.goto(target, { waitUntil: 'domcontentloaded', timeout: NAV_TIMEOUT });
	await page.waitForLoadState('networkidle', { timeout: SETTLE }).catch(() => {});
	let html = await page.content();
	if (html.length > MAX_HTML) html = html.slice(0, MAX_HTML);
	return { status: resp ? resp.status() : 200, html, final: page.url() };
}

http.createServer(async (req, res) => {
	const u = new URL(req.url, 'http://x');
	if (u.pathname === '/health') {
		res.writeHead(200, { 'Content-Type': 'text/plain' });
		return res.end('ok');
	}
	if (u.pathname !== '/read' || req.method !== 'GET') {
		res.writeHead(404);
		return res.end('not found');
	}
	let target;
	try {
		target = new URL(u.searchParams.get('url') || '');
	} catch {
		res.writeHead(400);
		return res.end('not a web address');
	}
	if (target.protocol !== 'http:' && target.protocol !== 'https:') {
		res.writeHead(400);
		return res.end('not a web address');
	}
	if (busy >= MAX_BUSY) {
		res.writeHead(503);
		return res.end('busy');
	}
	busy++;
	const started = Date.now();
	try {
		const r = await read(target.href);
		res.writeHead(r.status >= 400 ? r.status : 200, { 'Content-Type': 'text/html; charset=utf-8', 'X-Final-URL': r.final });
		res.end(r.html);
		console.log(`${new Date().toISOString()} ${r.status} ${Date.now() - started}ms ${target.host}`);
	} catch (e) {
		res.writeHead(502, { 'Content-Type': 'text/plain' });
		res.end(String(e.message || e).split('\n')[0].slice(0, 200));
		console.log(`${new Date().toISOString()} error ${Date.now() - started}ms ${target.host}: ${String(e.message || e).split('\n')[0]}`);
	} finally {
		busy--;
	}
}).listen(PORT, () => console.log(`reader on :${PORT}`));
