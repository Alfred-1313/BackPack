import * as api from '../api.js';
import { copyText, flashCopied, esc } from '../lib/dom.js';

export function bindExtraTransports(root, options) {
  const step = root.querySelector('.step3rev').closest('.step');
  const panel = document.createElement('section');
  panel.className = 'extra-transport'; panel.hidden = true;
  step.append(panel);
  const setActive = active => {
    panel.hidden = !active;
    step.classList.toggle('extra-active', active);
    root.classList.toggle('extra-active', active);
  };
  const button = (label, click) => {
    const b = document.createElement('button'); b.type = 'button'; b.className = 'tr'; b.textContent = label;
    b.onclick = click; return b;
  };
  function select(value) {
    setActive(true);
    panel.replaceChildren(button('Back to transport families', () => setActive(false)));
    const grid = document.createElement('div'); grid.className = 'trgrid';
    panel.append(grid);
    const choices = value === 'gost-direct' ? [{value,label:'GOST Direct'}] : options;
    for (const item of choices) {
      const b = button(item.label, () => select(item.value));
      b.classList.toggle('on', item.value === value); grid.append(b);
    }
    if (!value) return;
    const gost = value === 'gost-direct';
    const title = choices.find(o => o.value === value)?.label || value;
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
        ['peer','Kharej IPv4'],...(gost ? [['ingress','Public TCP port on Iran'],['port','GOST TLS port on Kharej'],['target','Service port on Kharej (127.0.0.1)']] : [['omniport','Kharej tunnel port (optional)']])];
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
    const fam = root.querySelector('.fam'), grid = root.querySelector('.step3direct .trgrid');
    if (fam && !fam.querySelector('[data-extra]')) {
      const b=button('OmniTunnel',()=>select(null)); b.dataset.extra='omni'; fam.append(b);
    }
    if (grid && !grid.querySelector('[data-extra]')) {
      for (const item of [...options,{value:'gost-direct',label:'GOST Direct (Relay + TLS)'}]) {
        const b=button(item.label,()=>select(item.value)); b.dataset.extra=item.value; grid.append(b);
      }
    }
  }
  attach();
  root.addEventListener('pick',()=>{setActive(false);attach();});
  root.addEventListener('step',()=>setActive(false));
  return attach;
}
