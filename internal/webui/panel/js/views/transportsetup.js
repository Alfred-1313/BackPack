import * as api from '../api.js';
import { copyText, flashCopied, esc } from '../lib/dom.js';

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
    const title = item.label;
    const form = document.createElement('form');
    form.innerHTML = '<h3>'+esc(title)+'</h3><label>On this server<select name="role"><option value="setup">Iran - create tunnel</option><option value="join">Kharej - apply setup token</option></select></label><div data-fields></div><button type="submit" class="nb solid2">Create tunnel</button><pre aria-live="polite"></pre><button type="button" class="nb" data-copy hidden>Copy setup token</button>';
    panel.append(form);
    const result = form.querySelector('pre'), copy = form.querySelector('[data-copy]');
    const submit = form.querySelector('[type="submit"]');
    const role = form.elements.role;
    let token = '';
    const refresh = () => {
      const join = role.value === 'join';
      const fields = join ? [['token','Setup token']] : [['name','Tunnel name'],...(!gost ? [['local','Iran IPv4']] : []),
        ['peer','Kharej IPv4'],...(gost ? [['ingress','Public TCP port on Iran'],['port','GOST TLS port on Kharej'],['target','Service port on Kharej (127.0.0.1)']] : [['omniport',value === 'gre' ? 'GRE key (optional)' : 'Kharej tunnel port (optional)']])];
      form.querySelector('[data-fields]').innerHTML = fields.map(([key,label]) => '<label>'+label+'<input '+(key === 'omniport' ? '' : 'required')+' autocomplete="off" name="'+key+'"></label>').join('');
      submit.textContent = join ? 'Apply setup token' : 'Create tunnel';
      result.textContent=''; copy.hidden=true;
    };
    refresh(); role.onchange = refresh;
    copy.onclick = async () => flashCopied(copy, await copyText(token));
    form.onsubmit = async event => {
      event.preventDefault();
      const f = Object.fromEntries(new FormData(form));
      submit.disabled = role.disabled = true; result.textContent='Working...'; copy.hidden=true;
      try {
        if (gost) {
          const {role: action, ...fields} = f;
          const data = await api.gostDirect({...fields, action});
          token = data.token;
          result.textContent = data.unit + (token ? '\nOn Kharej apply the token here or with backpack gost-direct join.\n'+token : '');
        } else {
          const args = f.role === 'join' ? ['join'] : ['setup',value,f.name,f.local,f.peer];
          if (f.role === 'setup' && f.omniport?.trim()) args.push(f.omniport.trim());
          const data = await api.omniRun(args,f.token || '');
          result.textContent = data.output.replace(/\x1b\[[0-9;]*m/g,'').trim() || 'Done';
          token = result.textContent.split(/\r?\n/).map(s=>s.trim()).find(s=>/^[A-Za-z0-9+/]{16,}={0,2}$/.test(s)) || '';
        }
        copy.hidden = !token;
      } catch (err) { result.textContent = err.message; }
      finally { submit.disabled = role.disabled = false; }
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
        card.innerHTML = '<span class="tn2"><b>'+esc(item.label)+'</b><span class="key2">'+esc(item.value)+'</span></span>';
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
