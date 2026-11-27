/* A tunnel's setup link — what builds its other end.
 *
 * CLI: Manage → Manage Tunnels → Setup Link.
 */

import { esc } from '../lib/dom.js';
import { flag } from '../lib/format.js';
import * as api from '../api.js';
import * as store from '../store.js';
import { openScreen } from '../ui/screen.js';
import { oops } from '../ui/toast.js';
import { setupLinkHTML, bindSetupLink } from '../ui/setuplink.js';

export function shareView(ctx) {
  const name = ctx.params.name;
  openScreen('share', {
    pick: '.dlg.share',
    bind: async (root, close) => {
      const t = store.tunnel(name);
      const fl = root.querySelector('.dh .fl');
      if (fl) fl.textContent = flag(t?.peerCountry) || flag(t?.country) || '·';
      const sub = root.querySelector('.dh .ttl small');
      if (sub) sub.textContent = name;
      const body = root.querySelector('#shareBody');
      bindSetupLink(body);
      if (t?.transport === 'matrix') {
        const title = root.querySelector('.dh .ttl b');
        if (title) title.textContent = 'Matrix nodes & ports';
        await bindMatrix(body, name);
        ctx.setTeardown(close);
        return;
      }

      const draw = async host => {
        try {
          const info = await api.tunnelLink(name, host);
          body.innerHTML = setupLinkHTML(info) + `
            <div class="sh-host"><label for="shHost">Address it names</label>
              <input id="shHost" value="${esc(info.host || '')}" placeholder="this server's IP or domain" spellcheck="false">
              <button type="button" id="shRedo">Rebuild</button></div>`;
        } catch (e) {
          body.innerHTML = `<div class="sh-wait">${esc(e.message)}</div>`;
        }
      };
      body.addEventListener('click', ev => {
        if (ev.target.closest('#shRedo')) draw(body.querySelector('#shHost')?.value.trim());
      });
      await draw('');
      ctx.setTeardown(close);
    },
  }).catch(oops);
}

async function bindMatrix(body, name) {
  async function refresh() {
    const state = await api.matrixStatus(name);
    const main = state.Role === 'main';
    body.innerHTML = `<div class="sl">
      <div class="sl-h"><b>${main ? 'Main' : 'Node'} · ${esc(state.IP)}</b><small>${esc(state.Subnet)}</small></div>
      ${state.Peers.map(peer => `<div class="sh-host">
        <span>${esc(peer.Name)} · ${esc(peer.IP)} · ${peer.Online ? 'Online' : 'Offline'}</span>
        ${main ? `<button type="button" data-matrix-remove="${esc(peer.Name)}">Remove</button>` : ''}
      </div>`).join('')}
      ${main ? '<div class="sh-host"><button type="button" data-matrix-invite>Invite a node</button></div>'
        : '<div class="tl-note">Manage membership from the Matrix Main panel.</div>'}
      <div data-matrix-link></div>
      <form data-matrix-edit class="f3">
        <label for="matrixMappings">Forwarded ports</label>
        <input id="matrixMappings" value="${esc((state.Ports || []).join(', '))}" placeholder="443 or 443=10.203.42.1:8443">
        <label><input type="checkbox" id="matrixUDP" ${state.AcceptUDP ? 'checked' : ''}> Forward UDP as well as TCP</label>
        <div class="sh-host"><button type="submit">Save forwarding</button></div>
      </form>
    </div>`;
  }
  body.addEventListener('click', async event => {
    const control = event.target.closest('[data-matrix-invite], [data-matrix-remove]');
    if (!control || control.disabled) return;
    control.disabled = true;
    try {
      if (control.hasAttribute('data-matrix-invite')) {
        body.querySelector('[data-matrix-link]').innerHTML = setupLinkHTML(await api.matrixInvite(name));
      } else if (window.confirm('Remove this Matrix node?')) {
        await api.matrixEdit({ name, removePeer: control.dataset.matrixRemove });
        await refresh();
      }
    } catch (err) { oops(err); }
    finally { control.disabled = false; }
  });
  body.addEventListener('submit', async event => {
    if (!event.target.matches('[data-matrix-edit]')) return;
    event.preventDefault();
    const button = event.target.querySelector('button');
    if (button.disabled) return;
    button.disabled = true;
    try {
      await api.matrixEdit({ name, ports: body.querySelector('#matrixMappings').value,
        acceptUdp: body.querySelector('#matrixUDP').checked });
      await refresh();
    } catch (err) { oops(err); }
    finally { button.disabled = false; }
  });
  await refresh();
}
