/* A tunnel's traffic limit — how much it may carry, in and out together,
 * before the engine takes it offline. Opened from the pencil in the card's
 * bottom band.
 *
 * The count is the tunnel's own total, carried over every restart and update,
 * so what this shows as used is what the limit is held against. The engine
 * enforces it (cmd/quota.go); this only writes the number. Reset usage sets
 * the count back to zero (#106) on either end; the Iran end's limit stays.
 */

import { esc } from '../lib/dom.js';
import { bytes as fmtBytes } from '../lib/format.js';
import * as api from '../api.js';
import * as store from '../store.js';
import { confirmBox } from '../ui/confirm.js';
import { openScreen } from '../ui/screen.js';
import { oops, toast } from '../ui/toast.js';

const GB = 1024 ** 3, TB = 1024 ** 4;
/* "5 TB", not "5.00 TB": a limit is a round number somebody chose. */
const bytes = n => fmtBytes(n).replace(/\.0+(?= )/, '').replace(/(\.\d*?)0+(?= )/, '$1');
const PRESETS = [
  { v: 0, label: 'Unlimited', note: 'no limit' },
  { v: 50 * GB, label: '50 GB' }, { v: 100 * GB, label: '100 GB' }, { v: 500 * GB, label: '500 GB' },
  { v: 1 * TB, label: '1 TB' }, { v: 2 * TB, label: '2 TB' }, { v: 5 * TB, label: '5 TB' }, { v: 10 * TB, label: '10 TB' },
];
const ADDS = [10 * GB, 100 * GB, 1 * TB];

const pctOf = (used, lim) => (lim ? Math.min(1, used / lim) : 0);
const toneOf = (used, lim) => {
  if (!lim) return 'none';
  const p = used / lim;
  return p >= 0.9 ? 'er' : p >= 0.7 ? 'wr' : 'ok';
};
const resetRow = used => `<div class="qt-reset"><span>Used so far <b>${esc(bytes(used))}</b></span>
  <button type="button" id="qtReset"${used ? '' : ' disabled'}>Reset usage</button></div>`;
const TONE_COLOR = { ok: 'var(--ac)', wr: 'var(--wr)', er: 'var(--er)', none: 'var(--mu)' };

export function quotaView(ctx) {
  const name = ctx.params.name;
  const mode = ctx.query.get('proxy');
  const subject = mode ? 'the proxy' : 'the tunnel';
  openScreen('quota', {
    pick: '.dlg.quota',
    bind: async (root, close) => {
      const sub = root.querySelector('.dh .ttl small');
      if (sub) sub.textContent = name;
      const fl = root.querySelector('.dh .fl');
      if (fl) fl.innerHTML = '<svg viewBox="0 0 24 24" style="width:17px;height:17px"><path d="M4 18a8 8 0 1116 0"/><path d="M12 18l4.5-6"/><circle cx="12" cy="18" r="1.4"/></svg>';
      const body = root.querySelector('#qtBody');
      const save = root.querySelector('#qtSave');

      let q, label = name;
      if (mode) root.querySelector('#qtNote').textContent = 'In and out together. The proxy keeps its usage across restarts.';
      /* A count the engine keeps in memory can only be zeroed with the tunnel
       * stopped, so this restarts it: said before, not discovered after. */
      const resetUsage = async () => {
        const btn = root.querySelector('#qtReset');
        if (!await confirmBox({
          title: `Reset the usage of <q>${esc(name)}</q>?`,
          body: `Usage goes back to zero${q.limit ? ' and the whole limit is available again' : ''}. `
              + 'The tunnel restarts briefly; the lifetime total is kept.',
          go: 'Reset usage',
          danger: true,
        })) return;
        if (btn) btn.disabled = true;
        try {
          const was = q;
          await api.resetTunnelUsage(name);
          toast(was.hit ? `${name} starts from zero — it is coming back online.` : `${name} starts counting from zero.`);
          store.refresh();
          close();
        } catch (e) { oops(e); if (btn) btn.disabled = false; }
      };
      try {
        if (mode) {
          const info = await api.mtProxySetup(name, mode);
          if (!info.quota) throw new Error('This proxy is no longer configured.');
          q = info.quota;
          label = q.label;
          if (sub) sub.textContent = `${name} · ${label}`;
        } else {
          q = await api.tunnelQuota(name);
        }
      } catch (e) {
        body.innerHTML = `<div class="qt-wait">${esc(e.message)}</div>`;
        if (save) save.hidden = true;
        return;
      }
      if (!mode && !q.settable) {
        body.innerHTML = `<div class="qt-state"><i></i><span><b>This is the kharej end.</b> A traffic limit is set on the
          Iran end of the tunnel — the one users connect to — and this end follows whatever it lets through.</span></div>
          ${resetRow(q.used || 0)}`;
        if (save) save.hidden = true;
        body.addEventListener('click', ev => { if (ev.target.closest('#qtReset')) resetUsage(); });
        return;
      }

      let want = q.limit || 0;
      let unit = want && want % TB === 0 ? TB : GB;

      const draw = () => {
        const used = q.used || 0;
        const tone = toneOf(used, want);
        const hit = q.hit && want === q.limit;
        const pct = hit ? 1 : pctOf(used, want);
        const left = want && !hit ? Math.max(0, want - used) : 0;
        const custom = !PRESETS.some(p => p.v === want);
        const stateBox = q.hit
          ? `<div class="qt-state er"><i></i><span><b>Offline — the limit is used up.</b> Nothing passes until it is raised;
              ${subject} can carry traffic again when you raise or remove the limit.</span></div>`
          : q.limit && used / q.limit >= 0.9
            ? `<div class="qt-state wr"><i></i><span><b>Nearly used up.</b> ${subject[0].toUpperCase() + subject.slice(1)} stops carrying traffic at
                ${esc(bytes(q.limit))}, and stays offline until the limit is raised.</span></div>`
            : `<div class="qt-state"><i></i><span>${q.limit ? `Goes offline at <b>${esc(bytes(q.limit))}</b>; back when the limit is raised.` : '<b>No limit.</b>'}</span></div>`;
        body.innerHTML = `
          <div class="qt-hero">
            <div class="qt-ring ${want ? '' : 'inf'}" style="--p:${pct.toFixed(4)};--c:${TONE_COLOR[tone]}">
              <svg viewBox="0 0 120 120"><circle class="trk" cx="60" cy="60" r="52"/>
                <circle class="arc" cx="60" cy="60" r="52" pathLength="1000"/></svg>
              <div class="mid">${want
                ? `<b>${(pct * 100).toFixed(pct < 0.1 ? 1 : 0)}<em>%</em></b><small>used</small>`
                : '<b>∞</b><small>no limit</small>'}</div>
            </div>
            <div class="qt-nums">
              <div><span>Used</span><b>${esc(bytes(used))}</b></div>
              <div><span>Limit</span><b>${want ? esc(bytes(want)) : 'Unlimited'}</b></div>
              <div class="wide"><span>Left</span><b>${want ? (!left ? 'nothing — offline' : esc(bytes(left))) : '—'}</b></div>
            </div>
          </div>
          ${stateBox}
          <div class="qt-h">Limit</div>
          <div class="qt-presets">${PRESETS.map(p =>
            `<button type="button" data-v="${p.v}" class="${p.v === want ? 'on' : ''}">${p.label}${p.note ? `<small>${p.note}</small>` : ''}</button>`).join('')}</div>
          <div class="qt-custom">
            <input id="qtNum" type="number" min="0" step="any" inputmode="decimal" placeholder="Or type an amount"
              value="${custom && want ? +(want / unit).toFixed(12) : ''}">
            <div class="qt-unit">${[['GB', GB], ['TB', TB]].map(([l, u]) =>
              `<button type="button" data-u="${u}" class="${u === unit ? 'on' : ''}">${l}</button>`).join('')}</div>
          </div>
          <div class="qt-add"><span>Add to it</span>${ADDS.map(a =>
            `<button type="button" data-add="${a}">+ ${esc(bytes(a))}</button>`).join('')}</div>
          <div class="qt-preview ${want && want <= used ? 'er' : ''}" id="qtPrev">${preview(used)}</div>
          ${mode ? '' : resetRow(used)}`;
        if (save) save.disabled = want === (q.limit || 0);
      };

      const preview = used => {
        if (want === (q.limit || 0)) return '';
        if (!want) return `Saving removes the limit — <b>${subject} runs without one</b>.`;
        if (want <= used) return `<b>Limit already reached</b> — ${subject} stops carrying traffic on save.`;
        return `New limit <b>${esc(bytes(want))}</b> — <b>${esc(bytes(want - used))}</b> left${q.hit ? `; ${subject} can carry traffic again` : ''}.`;
      };

      const quantity = (number, scale) => {
        const amount = Math.round(number * scale);
        return Number.isSafeInteger(amount) && amount > 0 ? amount : null;
      };

      body.addEventListener('click', ev => {
        const b = ev.target.closest('button');
        if (!b) return;
        if (b.id === 'qtReset') { resetUsage(); return; }
        if (b.dataset.v !== undefined) { want = Number(b.dataset.v); draw(); return; }
        if (b.dataset.u) {
          unit = Number(b.dataset.u);
          const n = Number(body.querySelector('#qtNum')?.value);
          if (n > 0) want = quantity(n, unit) ?? want;
          draw();
          return;
        }
        if (b.dataset.add) {
          want = quantity((want || q.used || 0) + Number(b.dataset.add), 1) ?? want;
          draw();
        }
      });
      body.addEventListener('input', ev => {
        if (ev.target.id !== 'qtNum') return;
        const amount = quantity(Number(ev.target.value), unit);
        if (amount == null) {
          ev.target.setCustomValidity('Enter a valid positive amount. Choose Unlimited to remove the limit.');
          if (save) save.disabled = true;
          return;
        }
        ev.target.setCustomValidity('');
        want = amount;
        // Only what the number changes: redrawing the whole pane would take the
        // field from under the cursor.
        body.querySelectorAll('.qt-presets button').forEach(x => x.classList.toggle('on', Number(x.dataset.v) === want));
        const prev = body.querySelector('#qtPrev');
        if (prev) { prev.innerHTML = preview(q.used || 0); prev.classList.toggle('er', want <= (q.used || 0)); }
        const lim = body.querySelector('.qt-nums > div:nth-child(2) b');
        if (lim) lim.textContent = bytes(want);
        if (save) save.disabled = want === (q.limit || 0);
      });
      body.addEventListener('change', ev => {
        if (ev.target.id === 'qtNum' && ev.target.validity.valid) draw();
      });

      save?.addEventListener('click', async () => {
        save.disabled = true;
        try {
          const was = q;
          q = mode ? await api.mtProxyAction(name, { action: 'quota', id: q.id, limit: want }, mode)
            : await api.setTunnelQuota(name, want);
          toast(!q.limit ? `${label} has no traffic limit now.`
            : was.hit && !q.hit ? `${label} has ${bytes(q.limit - q.used)} available again.`
              : `${label} traffic limit saved at ${bytes(q.limit)}.`);
          store.refresh();
          close();
        } catch (e) { oops(e); save.disabled = false; }
      });

      draw();
      ctx.setTeardown(close);
    },
  }).catch(oops);
}
