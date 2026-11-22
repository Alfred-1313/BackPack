import { omniOptions, omniRun } from '../api.js';
import { copyText, flashCopied } from '../lib/dom.js';
import { openScreen } from '../ui/screen.js';

const actions = [
  ['list','List tunnels'], ['setup','Create tunnel on Iran','transport','name','local','peer'],
  ['join','Apply setup token on Kharej','token'], ['status','Tunnel status','name'],
  ['forward','Add port forward','name','protocol','mapping'], ['remove-forward','Remove port forward','name','port'],
  ['mux-forward','Add Reverse Mux forward','name','port','host','target'], ['mux-join','Apply Reverse Mux token','token'],
  ['mux-remove','Remove Reverse Mux forward','name','port'], ['mux-off','Disable Reverse Mux forwarding','name'],
  ['start','Start or restart tunnel','name'], ['stop','Stop tunnel','name'], ['remove','Remove tunnel','name']
];
const labels = {transport:'Transport',name:'Tunnel name',local:'Iran IPv4',peer:'Kharej IPv4',token:'Setup token',
  protocol:'Protocol',mapping:'Public port or public:target',port:'Public port',host:'Target host on Kharej',target:'Target port'};

export function omniView() {
  return openScreen('omni', { bind: async root => {
    const form = root.querySelector('form'), action = form.elements.action;
    const fields = root.querySelector('[data-fields]'), result = root.querySelector('pre');
    const copy = root.querySelector('[data-copy]'), submit = form.querySelector('button');
    let transports = [], token = '';
    try { transports = await omniOptions(); } catch (e) { result.textContent = e.message; submit.disabled = true; return; }
    for (const [value,title] of actions) action.add(new Option(title,value));
    function render() {
      const [,title,...keys] = actions[action.selectedIndex];
      fields.replaceChildren(); result.textContent = ''; copy.hidden = true; token = ''; submit.textContent = title;
      for (const key of keys) {
        const label = document.createElement('label'); label.textContent = labels[key];
        const opts = key === 'transport' ? transports : key === 'protocol' ? [{label:'TCP',value:'tcp'},{label:'UDP',value:'udp'}] : null;
        const input = document.createElement(opts ? 'select' : key === 'token' ? 'textarea' : 'input');
        if (opts) for (const o of opts) input.add(new Option(o.label,o.value));
        input.name = key; input.required = true; input.autocomplete = 'off';
        label.append(input); fields.append(label);
      }
    }
    action.onchange = render; render();
    copy.onclick = async () => flashCopied(copy, await copyText(token));
    form.onsubmit = async e => {
      e.preventDefault();
      const [command,title,...keys] = actions[action.selectedIndex];
      const val = k => form.elements[k].value.trim();
      if (['stop','remove','start','remove-forward','mux-remove','mux-off'].includes(command) && !window.confirm(title+' on '+val('name')+'?')) return;
      const args = [command,...keys.filter(k => k !== 'token').map(val)];
      const input = keys.includes('token') ? val('token') : '';
      if (command === 'remove-forward') args.push('--force');
      action.disabled = submit.disabled = true; result.textContent = 'Running...'; copy.hidden = true;
      try {
        const {output} = await omniRun(args,input);
        const text = output.replace(/\x1b\[[0-9;]*m/g,'').trim();
        result.textContent = text || (command === 'list' ? 'No routed tunnels on this server.' : 'Done');
        token = text.split(/\r?\n/).map(s => s.trim()).find(s => /^[A-Za-z0-9+/]{16,}={0,2}$/.test(s)) || '';
        copy.hidden = !token;
      } catch (err) { result.textContent = err.message; }
      finally { action.disabled = submit.disabled = false; }
    };
    form.requestSubmit();
  }});
}
