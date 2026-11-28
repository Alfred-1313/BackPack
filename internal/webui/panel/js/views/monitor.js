/* Alerts and health check — screens that were designed together and share
 * one stylesheet.
 *
 * CLI: Manage → Health Check, and the alert history.
 */

import { $$, esc } from '../lib/dom.js';
import { ago } from '../lib/format.js';
import * as api from '../api.js';
import * as store from '../store.js';
import { openScreen } from '../ui/screen.js';
import { oops, toast } from '../ui/toast.js';
import { svg } from '../lib/icons.js';

/* ---- alerts -------------------------------------------------------------- */
const clock = d => d.toTimeString().slice(0, 5);

/* Today, Yesterday, or the date — the feed is read by day. */
function dayLabel(d) {
  const start = x => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((start(new Date()) - start(d)) / 864e5);
  if (days === 0) return 'Today';
  if (days === 1) return 'Yesterday';
  return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
}

export function alertsView(ctx) {
  openScreen('monitor', {
    pick: '.dlg.al2',
    bind: async (root, close) => {
      let data;
      try { data = await api.alerts(); } catch (e) { return oops(e); }

      /* The template's own shapes — .ah2/.ai2 for what is firing, .daysep/.ev2
         for the feed — because they carry the insets the dialog was designed
         with. The rows this used to write (.arow/.erow) had two pixels either
         side, so every line sat pressed against the dialog's edge. */
      const active = root.querySelector('.active');
      const list = data.active || [];
      if (active) {
        active.classList.toggle('calm', !list.length);
        active.innerHTML = list.length
          ? `<div class="ah2">Firing now · ${list.length}</div>` +
            list.map(a => `<div class="ai2"><i>${svg('warn')}</i><span>${esc(a)}</span></div>`).join('')
          : `<div class="ai2 ok"><i>${svg('check')}</i><span>Nothing is firing right now</span></div>`;
      }

      /* Newest first. The file keeps them oldest first, and drawing it in that
         order put the last thing that happened at the bottom of a list that
         opens at the top — the current state was the one line you had to
         scroll for. */
      const feed = root.querySelector('.evs');
      if (feed) {
        const evs = (data.events || []).slice().reverse();
        let day = '';
        feed.innerHTML = evs.map(e => {
          const d = new Date(e.time);
          const label = dayLabel(d);
          const sep = label !== day ? `<div class="daysep">${esc(label)}</div>` : '';
          day = label;
          return sep + `<div class="ev2"><span class="tm" title="${esc(d.toLocaleString())}">` +
            /* ago() takes unix seconds and the file holds RFC 3339 — handed
               the string, every line said "NaN d ago". */
            `${esc(clock(d))}<em>${esc(ago(d.getTime() / 1000))}</em></span>` +
            `<span class="ms">${esc(e.message)}</span></div>`;
        }).join('') || `<div class="ev2 none"><span class="ms">No events recorded yet.</span></div>`;
        const body = root.querySelector('.body4');
        if (body) body.scrollTop = 0;
      }
      ctx.setTeardown(close);
    },
  }).catch(oops);
}

/* ---- health check --------------------------------------------------------
   The API orders engine checks first, followed by system and tunnel diagnostics.
   A check with a Fix carries it; one without does not. */
export function healthView(ctx) {
  openScreen('monitor', {
    pick: '.dlg.hc2',
    bind: async (root, close) => {
      /* .body4 is the scrolling area under the header. Falling back to the
         dialog itself would take the header and the close button with it. */
      const body = root.querySelector('.body4');
      if (!body) return;
      /* The whole read-and-render, so "Run again" can do exactly what opening
         the screen does — rather than reopening the screen, which fights the
         dialog's own lifecycle and left it closing itself. */
      const load = async () => {
        body.innerHTML = `<div class="hcrun">Running the checks…</div>`;

        let checks;
        try { checks = await api.health(); } catch (e) { return oops(e); }

        const groups = new Map();
        for (const c of checks) {
          const g = c.Group || c.group || 'System';
          if (!groups.has(g)) groups.set(g, []);
          groups.get(g).push(c);
        }
        /* The server says ok, warn, fail or info (handlers_monitoring.go). This
           read "error" for a failure, which the server never sends: a failed
           check — the engine binary missing, say — was drawn with the green
           dot and the tally said "0 broken". Info is a fact, not a pass. */
        const lvl = c => {
          const l = String(c.Level || c.level || 'ok').toLowerCase();
          return l === 'fail' ? 'error' : l;
        };
        const counts = { ok: 0, warn: 0, error: 0 };
        checks.forEach(c => { counts[lvl(c)] = (counts[lvl(c)] || 0) + 1; });
        const versionLabel = c => c.versionLabel || c.Version || c.version;

        body.innerHTML =
          `<div class="hcsum">
            <span class="hcpill ok">${counts.ok} passed</span>
            <span class="hcpill wr">${counts.warn || 0} to look at</span>
            <span class="hcpill er">${counts.error || 0} broken</span>
          </div>` +
          [...groups].map(([g, list]) => `
            <div class="hcgroup">
              <div class="hcg">${esc(g)}</div>
              ${list.map(c => `
                <div class="hcitem ${lvl(c)}">
                  <span class="hcdot"></span>
                  <div class="hctx">
                    <b>${esc(c.Name || c.name)}</b>
                    ${versionLabel(c) ? `<small class="hcversion">${esc(versionLabel(c))}</small>` : ''}
                    <span>${esc(c.Detail || c.detail || '')}</span>
                    ${(c.Fix || c.fix) ? `<i class="hcfix">${esc(c.Fix || c.fix)}</i>` : ''}
                  </div>
                  ${c.coreUpdate ? `<button type="button" class="bt hcupdate" data-version="${esc(c.version || '')}">Update core</button>` : ''}
                </div>`).join('')}
            </div>`).join('');
        body.querySelector('.hcupdate')?.addEventListener('click', ev => {
          xrayCoreDialog(ev.currentTarget.dataset.version, load);
        });
        /* The footer counted the preview's own example run. */
        const foot = root.querySelector('.df .note');
        if (foot) {
          const n = checks.length;
          foot.textContent = `${n} check${n === 1 ? '' : 's'} · ` +
            `${counts.warn || 0} warning${counts.warn === 1 ? '' : 's'} · ` +
            `${counts.error || 0} failure${counts.error === 1 ? '' : 's'}`;
        }
      };
      await load();

      /* Diagnose runs on the server; asking again is just asking again. */
      $$('button', root)
        .filter(b2 => /^run again$/i.test((b2.textContent || '').trim()))
        .forEach(b2 => b2.addEventListener('click', () => load()));

      ctx.setTeardown(close);
    },
  }).catch(oops);
}

function xrayCoreDialog(current, reload) {
  const dialog = document.createElement('dialog');
  dialog.className = 'dlg xray-core';
  dialog.setAttribute('aria-labelledby', 'xray-core-title');
  dialog.innerHTML = `
    <form>
      <div class="dh">
        <div class="ttl">
          <strong id="xray-core-title">Update Xray core</strong>
          <small>${current ? `Installed on this server · ${esc(current)}` : 'Installed version unavailable'}</small>
        </div>
        <button class="x" type="button" aria-label="Close">${svg('close')}</button>
      </div>
      <div class="panes pane">
        <div class="f">
          <label for="xray-release">Latest releases</label>
          <select id="xray-release" class="sel" disabled aria-describedby="xray-release-note">
            <option value="">Select a version</option>
          </select>
          <div class="hint" id="xray-release-note" aria-live="polite">Loading official releases…</div>
        </div>
        <div class="f">
          <label for="xray-custom">Custom version</label>
          <input id="xray-custom" type="text" placeholder="e.g. 26.7.28" maxlength="64"
            spellcheck="false" autocomplete="off" aria-describedby="xray-custom-note">
          <div class="hint" id="xray-custom-note">A custom version takes priority over the selected release.</div>
        </div>
        <p class="xr-note">Running tunnels will use the new core the next time they start.</p>
        <p class="xr-status" role="status" aria-live="polite"></p>
      </div>
      <div class="df">
        <span class="sp"></span>
        <button type="button" class="bt xr-dismiss">Cancel</button>
        <button type="submit" class="bt primary" disabled>Update core</button>
      </div>
    </form>`;

  const select = dialog.querySelector('select');
  const custom = dialog.querySelector('input');
  const submit = dialog.querySelector('[type="submit"]');
  const dismiss = dialog.querySelector('.xr-dismiss');
  const status = dialog.querySelector('.xr-status');
  const releaseNote = dialog.querySelector('#xray-release-note');
  let running = false;
  let loaded = false;
  const chosen = () => custom.value.trim() || select.value;
  const controls = () => {
    select.disabled = running || !loaded;
    custom.disabled = running;
    submit.disabled = running || !chosen();
    dismiss.textContent = running ? 'Close' : 'Cancel';
  };
  const showStatus = (message, error = false) => {
    status.textContent = message;
    status.classList.toggle('error', error);
  };
  const watch = async job => {
    while (dialog.open && job.running) {
      showStatus(job.step || `Preparing ${job.target}…`);
      await new Promise(resolve => setTimeout(resolve, 1000));
      if (!dialog.open) return;
      job = await api.xrayCoreStatus(job.id);
    }
    if (!dialog.open) return;
    if (!job.succeeded) throw new Error(job.error || 'The Xray update was interrupted.');
    dialog.close();
    toast(`Xray ${job.target} installed`);
    await reload();
  };
  const run = async start => {
    running = true;
    controls();
    try {
      const job = await start();
      if (job && dialog.open) await watch(job);
    } catch (e) {
      showStatus(e.message, true);
    } finally {
      running = false;
      controls();
    }
  };
  select.addEventListener('change', controls);
  custom.addEventListener('input', controls);
  dialog.querySelector('form').addEventListener('submit', event => {
    event.preventDefault();
    const version = chosen();
    if (running || !version) return;
    showStatus(`Starting update to ${version}…`);
    run(() => api.xrayCoreUpdate(version));
  });
  dismiss.addEventListener('click', () => dialog.close());
  dialog.querySelector('.dh button').addEventListener('click', () => dialog.close());
  dialog.addEventListener('keydown', event => {
    if (event.key === 'Escape') event.stopPropagation();
  });
  dialog.addEventListener('close', () => dialog.remove(), { once: true });
  document.body.append(dialog);
  dialog.showModal();
  run(() => api.xrayCoreStatus());
  api.xrayReleases().then(releases => {
    for (const release of releases) {
      select.add(new Option(release.tag + (release.prerelease ? ' (pre-release)' : ''), release.tag));
    }
    loaded = releases.length > 0;
    releaseNote.textContent = loaded ? '' : 'No releases for this server.';
    controls();
  }).catch(e => {
    releaseNote.textContent = 'Could not load releases. You can enter a version manually.';
    releaseNote.title = e.message;
  });
}
