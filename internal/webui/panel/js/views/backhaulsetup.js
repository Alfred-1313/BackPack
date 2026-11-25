import * as api from '../api.js';
import { copyText, flashCopied } from '../lib/dom.js';

export function showBackhaulSetup(panel, mode, label) {
  const form = document.createElement('form');
  const tun = mode === 'tun-tcp' || mode === 'tun-ipx';
  const ipx = mode === 'tun-ipx';
  form.innerHTML = `<h3></h3>
    <label>Tunnel name<input name="name" required maxlength="40" autocomplete="off"></label>
    <label>${ipx ? 'Iran IPv4' : 'Iran address for Kharej to dial'}<input name="iranHost" required autocomplete="off"></label>
    ${ipx ? `<label>Kharej IPv4<input name="kharejIP" required autocomplete="off"></label>
      <label>IPX profile<select name="ipxProfile">
        <option value="tcp">TCP</option><option value="udp">UDP</option><option value="icmp">ICMP</option>
        <option value="gre">GRE</option><option value="ipip">IP-in-IP</option><option value="bip">BIP</option>
      </select></label>` : `<label>Tunnel port on Iran
        <input name="port" type="number" min="1" max="65535" required value="8443"></label>`}
    ${tun ? `<label>Iran tunnel IPv4/CIDR<input name="iranTunnel" required placeholder="10.43.0.1/30"></label>
      <label>Kharej tunnel IPv4/CIDR<input name="kharejTunnel" required placeholder="10.43.0.2/30"></label>` : ''}
    <label>Forwarded TCP ports (optional)<input name="ports" autocomplete="off" placeholder="443, 2053=8443"></label>
    <button type="submit" class="nb solid2">Create tunnel</button>
    <pre aria-live="polite"></pre>
    <button type="button" class="nb" data-copy hidden>Copy Kharej command</button>`;
  form.querySelector('h3').textContent = label;
  panel.append(form);

  const result = form.querySelector('pre');
  const copy = form.querySelector('[data-copy]');
  const submit = form.querySelector('[type="submit"]');
  let command = '';
  copy.onclick = async () => flashCopied(copy, await copyText(command));
  form.onsubmit = async ev => {
    ev.preventDefault();
    const body = Object.fromEntries(new FormData(form));
    body.transport = mode;
    if (body.port) body.port = Number(body.port);
    submit.disabled = true;
    copy.hidden = true;
    result.textContent = 'Creating the Backhaul tunnel on Iran?';
    try {
      const response = await api.backhaulCreate(body);
      command = response.apply;
      result.textContent = response.active
        ? 'Run the Setup command on Kharej. It contains this tunnel?s private token.'
        : 'The tunnel was configured. Check the Backhaul service log on Iran for startup status.';
      copy.hidden = !command;
    } catch (err) {
      result.textContent = err.message;
    } finally {
      submit.disabled = false;
    }
  };
}
