import * as api from '../api.js';
import { showBackhaulSetup } from './backhaulsetup.js';
import { esc } from '../lib/dom.js';
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
    if (item.backhaulTransport) {
      showBackhaulSetup(panel, item.backhaulTransport, item.label);
      return;
    }
    const value = item.value;
    const gost = value === 'gost-direct';
    const title = item.label;
    const form = document.createElement('form');
    const fields = [['name', 'Tunnel name'], ...(gost
      ? [['ingress', 'Public TCP port on Iran'], ['port', 'GOST TLS port on Kharej'], ['target', 'Service port on Kharej (127.0.0.1)']]
      : [['port', 'Kharej tunnel port (optional)']])];
    form.innerHTML = `<h3>${esc(title)}</h3>
      ${fields.map(([key, label]) => `<label>${label}<input ${key === 'port' && !gost ? '' : 'required'} autocomplete="off" name="${key}"></label>`).join('')}
      <button type="submit" class="nb solid2">Create tunnel</button>
      <div aria-live="polite"></div>`;
    panel.append(form);

    const result = form.querySelector('[aria-live]');
    const submit = form.querySelector('[type="submit"]');
    bindSetupLink(result);
    form.onsubmit = async event => {
      event.preventDefault();
      const f = Object.fromEntries([...new FormData(form)].map(([name, value]) => [name, value.trim()]));
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
    const reverse = root.querySelector('#trlist');
    const direct = root.querySelector('.step3direct .trgrid');
    const entries = [...options, {value:'gost-direct', label:'GOST Direct (Relay + TLS)', direction:'direct'}];
    for (const [grid, direction] of [[reverse, 'reverse'], [direct, 'direct']]) {
      if (!grid || grid.querySelector('[data-extra]')) continue;
      grid.closest('.grp3').classList.add('transport-choices');
      for (const item of entries.filter(item => item.direction === direction)) {
        const card = document.createElement('button');
        card.type = 'button';
        card.className = direction === 'reverse' ? 'tr' : 'dc';
        card.dataset.extra = item.value;
        card.title = item.desc || '';
        const id = item.backhaulTransport || item.value === 'gost-direct' ? item.value : 'omni-' + item.value;
        card.innerHTML = '<span class="tn2">' + transportOption(id, item.label, item.value) + '</span>';
        card.onclick = () => select(item, card);
        grid.append(card);
      }
    }
  }
  attach();
  root.addEventListener('pick', close);
  root.addEventListener('step', close);
  return attach;
}
