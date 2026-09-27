'use strict';
let csrf = '', flow = null, timer = null, baseURL = '';
const $ = id => document.getElementById(id);
const message = text => { $('notice').textContent = text; };
async function api(path, method = 'GET', body) {
  const response = await fetch('/api/' + path, {method, credentials: 'same-origin', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf}, body: body === undefined ? undefined : JSON.stringify(body)});
  const data = await response.json();
  if (!response.ok) { if (response.status === 401) signedOut(); throw new Error(data.error || 'Request failed'); }
  return data;
}
function action(fn) { return async event => { if (event) event.preventDefault(); try { await fn(event); } catch (e) { message(e.message); } }; }
function signedOut() { csrf = ''; clearTimeout(timer); flow = null; $('login').hidden = false; $('app').hidden = true; $('logout').hidden = true; }
async function signedIn() { $('login').hidden = true; $('app').hidden = false; $('logout').hidden = false; message(''); await refresh(); await providers(); await refresh(); }
function element(tag, text) { const e = document.createElement(tag); e.textContent = text; return e; }
function button(text, fn) { const b = element('button', text); b.addEventListener('click', action(fn)); return b; }
async function refresh() {
  const d = await api('dashboard');
  const u = new URL(location.href); u.protocol = 'http:'; u.port = String(d.api_port); u.pathname = '/v1'; u.search = ''; u.hash = ''; baseURL = u.href;
  $('status').textContent = d.status; $('version').textContent = d.version ? 'Version ' + d.version : 'Version unavailable';
  document.querySelectorAll('.base-url').forEach(e => e.textContent = baseURL);
  document.querySelectorAll('.key-hint').forEach(e => e.textContent = d.key_hint);
  $('models').replaceChildren(...(d.models || []).map(m => element('li', m)));
  return d;
}
async function providers() {
  const [cards, accounts] = await Promise.all([api('providers'), api('accounts')]);
  $('provider-cards').replaceChildren();
  cards.forEach(p => { const card = element('article', ''); const connected = accounts.some(a => a.provider === p.id && !a.disabled); card.append(element('h2', p.name), element('p', p.description), element('p', connected ? 'Connected' : 'Not connected'), button(connected ? 'Reconnect' : 'Connect', () => connect(p))); $('provider-cards').append(card); });
  $('accounts').replaceChildren(); $('overview-accounts').replaceChildren(...accounts.map(a => element('li', a.provider + (a.email ? ' · ' + a.email : '') + ' · ' + (a.disabled ? 'Disabled' : a.status)))); if (!accounts.length) $('overview-accounts').append(element('li', 'No accounts connected yet. Open Providers to get started.'));
  if (!accounts.length) $('accounts').append(element('p', 'No accounts connected yet.'));
  accounts.forEach(a => { const card = element('article', ''); card.append(element('h3', a.provider), element('p', (a.email || 'Account') + (a.plan ? ' · ' + a.plan : '') + ' · ' + (a.disabled ? 'Disabled' : a.status)), button(a.disabled ? 'Enable' : 'Disable', async () => { await api('accounts', 'PATCH', {id:a.id, disabled:!a.disabled}); await providers(); }), button('Models', async () => { const models = await api('accounts/' + a.id + '/models'); message(models.length ? models.join(', ') : 'No models reported for this account.'); }), button('Disconnect', async () => { if (!confirm('Disconnect this account and remove its saved credential?')) return; await api('accounts', 'DELETE', {id:a.id, confirm:true}); await providers(); await refresh(); })); $('accounts').append(card); });
}
async function connect(p) {
  if (flow) { message('Finish the current login or wait for it to expire before starting another.'); return; }
  const result = await api('providers/' + p.id + '/connect', 'POST', {});
  flow = {...result, provider:p.id, name:p.name}; $('flow').hidden = false; $('flow-title').textContent = 'Connect ' + p.name;
  $('auth-link').href = flow.url; $('flow-status').textContent = 'Waiting for authentication'; $('callback-form').hidden = flow.device; $('cancel-flow').hidden = false;
  $('device-code').textContent = flow.user_code ? 'Device code: ' + flow.user_code : ''; $('callback').value = '';
  window.open(flow.url, '_blank', 'noopener,noreferrer');
  message('Complete sign-in in the provider page. If no tab opened, use the sign-in link below.'); poll();
}
async function poll() {
  if (!flow) return;
  try { const d = await api('providers/' + flow.provider + '/status/' + flow.id);
    if (d.status === 'connected') { $('flow-status').textContent = flow.provider === 'codex' ? 'OpenAI connected' : flow.name + ' connected'; $('callback-form').hidden = true; $('cancel-flow').hidden = true; flow = null; await providers(); await refresh(); message('Account connected. Your API is ready.'); return; }
    if (d.status === 'error') throw new Error('Authentication failed. Start a new connection.');
    timer = setTimeout(poll, 2500);
  } catch (e) { message(e.message); $('flow-status').textContent = 'Connection stopped'; $('cancel-flow').hidden = true; flow = null; }
}
async function copy(text) {
  if (navigator.clipboard && window.isSecureContext) { await navigator.clipboard.writeText(text); message('Copied.'); return; }
  // Clipboard API requires HTTPS; this local fallback supports Unraid HTTP.
  const t = document.createElement('textarea'); t.value = text; t.setAttribute('readonly', ''); document.body.append(t); t.select();
  const ok = document.execCommand('copy'); t.remove(); if (!ok) { window.prompt('Copy this value:', text); } else message('Copied.');
}
$('login-form').addEventListener('submit', action(async () => { const data = await api('login', 'POST', {password:$('password').value}); $('password').value = ''; csrf = data.csrf; await signedIn(); }));
$('logout').addEventListener('click', action(async () => { await api('logout', 'POST', {}); signedOut(); }));
$('callback-form').addEventListener('submit', action(async () => { if (!flow) return; await api('providers/' + flow.provider + '/callback/' + flow.id, 'POST', {url:$('callback').value.trim()}); $('callback').value = ''; message('Callback submitted. Waiting for credential storage.'); }));
document.querySelectorAll('[data-page]').forEach(b => b.addEventListener('click', action(async () => { document.querySelectorAll('.page').forEach(p => p.hidden = p.id !== b.dataset.page); document.querySelectorAll('[data-page]').forEach(n => n.classList.toggle('selected', n === b)); if (b.dataset.page === 'providers') await providers(); })));
document.querySelectorAll('.copy-url').forEach(b => b.addEventListener('click', action(() => copy(baseURL))));
document.querySelectorAll('.copy-key').forEach(b => b.addEventListener('click', action(async () => { const d = await api('key'); await copy(d.key); })));
$('rotate').addEventListener('click', action(async () => { if (!confirm('Existing clients using this key will stop working. Regenerate it?')) return; $('rotate').disabled = true; try { await api('key/rotate', 'POST', {confirm:true}); await refresh(); message('New key verified. Copy it into your clients.'); } finally { $('rotate').disabled = false; } }));
$('refresh-models').addEventListener('click', action(refresh));
$('test').addEventListener('click', action(async () => { const d = await refresh(); message(d.status === 'Running' ? 'API responds successfully.' : 'API is not ready yet.'); }));
$('refresh-logs').addEventListener('click', action(async () => { const lines = await api('logs'); $('log-lines').textContent = lines.join('\n') || 'No recent log events.'; }));
(async () => { try { const s = await api('session'); csrf = s.csrf; await signedIn(); } catch (e) { signedOut(); } })();

$('cancel-flow').addEventListener('click', action(async () => { if (!flow) return; await api('providers/' + flow.provider + '/cancel/' + flow.id, 'POST', {}); clearTimeout(timer); flow = null; $('flow').hidden = true; message('Login cancelled.'); }));
