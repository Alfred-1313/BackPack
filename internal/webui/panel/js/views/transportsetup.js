import * as api from '../api.js';
import { setupLinkHTML, bindSetupLink } from '../ui/setuplink.js';
import { transportOption } from '../lib/transports.js';

export function bindExtraTransports(root, options) {
  const step = root.querySelector('.step3rev').closest('.step');
  const panel = document.createElement('section');
  panel.className = 'extra-transport'; panel.hidden = true;
  let currentCard, previousNative;
  function close() {
    panel.hidden = true;
    step.classList.remove('extra-active');
    root.classList.remove('extra-active');
    if (currentCard?.parentElement) {
      const grid = currentCard.parentElement;
      currentCard.classList.remove('on');
      if (!grid.querySelector('.on') && previousNative?.parentElement === grid) previousNative.classList.add('on');
    }
    currentCard = previousNative = null;
  }

  function select(item, card) {
    const grid = card.parentElement;
    if (!currentCard) previousNative = grid.querySelector('[data-fn].on');
    for (const button of grid.children) button.classList.toggle('on', button === card);
    currentCard = card;
    grid.closest('.grp3').after(panel);
    panel.hidden = false;
    step.classList.add('extra-active');
    root.classList.add('extra-active');
    panel.replaceChildren();
    const value = item.value;
    const gost = value === 'gost-direct';
    const form = document.createElement('form');
    form.innerHTML = `<div class="grp3">
      <div class="gl3">The tunnel</div>
      <div class="fgrid">
        <div class="f3 full"><label>Tunnel name<input name="name" required maxlength="32" autocomplete="off"></label></div>
        ${item.direction === 'direct' ? `<div class="f3 full"><label>Kharej server address
          <input name="peerAddr" placeholder="Automatically detected" autocomplete="off"></label>
          <div class="hint">Leave blank to detect the address from the Setup connection.</div></div>` : ''}
        ${item.port ? `<div class="f3 full"><label>Tunnel port
          <input name="port" type="number" min="1" max="65535" ${gost ? 'required' : 'placeholder="Automatic"'}></label></div>` : ''}
        ${item.secret ? `<div class="f3 full"><label>Secret token</label>
          <div class="withb"><input name="secret" type="password" maxlength="512" placeholder="Automatically generated"
            autocomplete="new-password"><button type="button" class="mini3 rnd" data-secret-random>Random</button></div></div>` : ''}
        ${item.forward ? `<div class="f3 full"><label>${gost ? 'Forwarded TCP port' : 'Forwarded ports'}
          <input name="ports" autocomplete="off" ${gost ? 'required placeholder="443=8443"' : 'placeholder="443, 2053=8443"'}></label>
          <div class="hint">The first port is on Iran. An entry like 443=8443 sends it to port 8443 on Kharej.</div></div>` : ''}
        ${item.udp ? `<div class="f3 full"><label><span><input type="checkbox" name="acceptUdp"> Forward UDP as well as TCP</span></label></div>` : ''}
      </div>
      </div>
      <button type="submit" class="nb solid2">Create tunnel</button>
      <div aria-live="polite"></div>`;
    panel.append(form);

    const result = form.querySelector('[aria-live]');
    const submit = form.querySelector('[type="submit"]');
    bindSetupLink(result);
    const random = form.querySelector('[data-secret-random]');
    random?.addEventListener('click', async () => {
      random.disabled = true;
      try {
        form.elements.secret.value = (await api.tunnelToken()).token;
      } catch (err) {
        result.textContent = err.message;
      } finally {
        random.disabled = false;
      }
    });
    form.onsubmit = async event => {
      event.preventDefault();
      const f = Object.fromEntries(new FormData(form));
      f.acceptUdp = form.elements.acceptUdp?.checked || false;
      submit.disabled = true;
      result.textContent = 'Preparing the Setup command…';
      try {
        const setup = await api.routedPeerSetup({ ...f, transport: value });
        for (const field of form.elements) field.disabled = true;
        result.innerHTML = setupLinkHTML(setup);
      } catch (err) {
        result.textContent = err.message;
        submit.disabled = false;
      }
    };
  }
  function attach() {
    const grid = root.querySelector('.step3direct .trgrid');
    if (!grid || grid.querySelector('[data-extra]')) return;
    const entries = [...options, {value:'gost-direct', label:'GOST Direct (Relay + TLS)', direction:'direct',
      port:true, secret:true, forward:true}];
    grid.closest('.grp3').classList.add('transport-choices');
    for (const item of entries) {
      const card = document.createElement('button');
      card.type = 'button';
      card.className = 'dc';
      card.dataset.extra = item.value;
      card.title = item.desc || '';
      const id = item.value === 'gost-direct' ? item.value : 'omni-' + item.value;
      card.innerHTML = '<span class="tn2">' + transportOption(id, item.label, item.value) + '</span>';
      card.onclick = () => select(item, card);
      grid.append(card);
    }
  }
  attach();
  root.addEventListener('pick', close);
  root.addEventListener('step', close);
  return attach;
}
