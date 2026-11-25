import * as api from '../api.js';
import { showDaggerSetup } from './daggersetup.js';
import { showBackhaulSetup } from './backhaulsetup.js';
import { copyText, flashCopied, esc } from '../lib/dom.js';
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
    if (item.daggerTransport) {
      showDaggerSetup(panel, item.daggerTransport);
      return;
    }
    if (item.backhaulTransport) {
      showBackhaulSetup(panel, item.backhaulTransport, item.label);
      return;
    }
    const value = item.value;
    const gost = value === 'gost-direct';
    const title = item.label;
    const form = document.createElement('form');
    const fields = [['name', 'Tunnel name'], ...(!gost ? [['local', 'Iran IPv4']] : []),
      ['peer', 'Kharej IPv4'], ...(gost
        ? [['ingress', 'Public TCP port on Iran'], ['port', 'GOST TLS port on Kharej'], ['target', 'Service port on Kharej (127.0.0.1)']]
        : [['omniport', 'Kharej tunnel port (optional)']])];
    form.innerHTML = `<h3>${esc(title)}</h3>
      ${fields.map(([key, label]) => `<label>${label}<input ${key === 'omniport' ? '' : 'required'} autocomplete="off" name="${key}"></label>`).join('')}
      <button type="submit" class="nb solid2">Create tunnel</button>
      <pre aria-live="polite"></pre><button type="button" class="nb" data-copy hidden>Copy Kharej command</button>`;
    panel.append(form);
    const result = form.querySelector('pre'), copy = form.querySelector('[data-copy]');
    const submit = form.querySelector('[type="submit"]');
    const shellArg = text => "'" + text.replaceAll("'", "'\\''") + "'";
    let command = '';
    copy.onclick = async () => flashCopied(copy, await copyText(command));
    form.onsubmit = async event => {
      event.preventDefault();
      const f = Object.fromEntries([...new FormData(form)].map(([name, value]) => [name, value.trim()]));
      submit.disabled = true;
      result.textContent = 'Creating the tunnel on Iran…';
      copy.hidden = true;
      try {
        let token, output;
        if (gost) {
          const data = await api.gostDirect({ ...f, action: 'setup' });
          token = data.token;
          output = data.unit;
        } else {
          const args = ['setup', value, f.name, f.local, f.peer];
          if (f.omniport) args.push(f.omniport);
          const data = await api.omniRun(args);
          output = data.output.replace(/\x1b\[[0-9;]*m/g, '').trim();
          token = output.split(/\r?\n/).map(line => line.trim()).find(line => /^[A-Za-z0-9+/]{16,}={0,2}$/.test(line));
        }
        if (!token) {
          result.textContent = 'The server did not return a setup token.' + (output ? '\n' + output : '');
          return;
        }

        const join = gost ? 'sudo backpack gost-direct join' : 'sudo backpack omni join ' + shellArg(value);
        command = `printf '%s\\n' ${shellArg(token)} | ${join}`;
        result.textContent = 'Tunnel created on Iran. Run this command on Kharej.\n\n' + command;
        copy.hidden = false;
      } catch (err) {
        result.textContent = err.message;
      } finally {
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
        const id = item.daggerTransport || item.backhaulTransport || item.value === 'gost-direct' ? item.value : 'omni-' + item.value;
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
