import * as api from '../api.js';
import { copyText, flashCopied } from '../lib/dom.js';

export function showDaggerSetup(panel, transport) {
  const form = document.createElement('form');
  const number = 'type="number" min="1" max="65535"';
  const input = (name, label, attrs = 'required') =>
    `<label>${label}<input name="${name}" ${attrs}></label>`;

  form.innerHTML = `<h3></h3>
    ${input('name', 'Tunnel name', 'required maxlength="40"')}
    ${input('iranIP', 'Iran public IPv4')}
    ${input('port', 'Tunnel port on Iran', number + ' required value="8443"')}
    ${input('license', 'DaggerConnect license PSK', 'type="password" required autocomplete="off"')}
    ${input('ports', 'Forwarded ports (optional)', 'placeholder="443, udp:2053=53"')}
    ${transport === 'dc6' ? input('iranIPv6', 'Iran IPv6 endpoint') : ''}
    ${transport === 'http' || transport === 'https' ? `
      ${input('httpHost', 'HTTP host')}
      ${input('httpPath', 'HTTP path', 'required value="/"')}
    ` : ''}
    ${transport === 'xhttp' ? input('httpPath', 'HTTP path', 'required value="/"') : ''}
    ${transport === 'https' ? `
      ${input('certFile', 'Certificate path on Iran')}
      ${input('keyFile', 'Private key path on Iran')}
    ` : ''}
    ${transport === 'tun' ? `
      ${input('kharejIP', 'Kharej IPv4')}
      ${input('iranTunnelIP', 'Iran tunnel IPv4', 'required placeholder="10.200.5.1"')}
      ${input('kharejTunnelIP', 'Kharej tunnel IPv4', 'required placeholder="10.200.5.2"')}
      <label>TUN encapsulation
        <select name="tunProfile">
          <option value="tcp">TCP</option><option value="udp">UDP</option>
          <option value="icmp">ICMP</option><option value="gre">GRE</option>
          <option value="ipip">IP-in-IP</option><option value="bip">BIP</option>
        </select>
      </label>
      ${input('tunPort', 'TUN service port', number + ' required value="443"')}
    ` : ''}
    <button type="submit" class="nb solid2">Create tunnel</button>
    <pre aria-live="polite"></pre>
    <button type="button" class="nb" data-copy hidden>Copy Kharej command</button>`;
  form.querySelector('h3').textContent = 'DaggerConnect';
  panel.append(form);
  const result = form.querySelector('pre');
  const copy = form.querySelector('[data-copy]');
  const submit = form.querySelector('[type="submit"]');
  const profile = form.elements.tunProfile;
  if (profile) profile.addEventListener('change', () => {
    const port = form.elements.tunPort;
    port.disabled = !['tcp', 'udp'].includes(profile.value);
    port.closest('label').hidden = port.disabled;
  });

  let command = '';
  copy.onclick = async () => flashCopied(copy, await copyText(command));
  form.onsubmit = async event => {
    event.preventDefault();
    const body = Object.fromEntries(new FormData(form));
    body.transport = transport;
    body.port = Number(body.port);
    if (body.tunPort) body.tunPort = Number(body.tunPort);
    submit.disabled = true;
    copy.hidden = true;
    result.textContent = 'Creating the DaggerConnect service on Iran…';
    try {
      const response = await api.daggerCreate(body);
      command = response.apply;
      result.textContent = response.active
        ? 'Run the setup command on Kharej. It contains the license PSK.'
        : 'The service was created. Check its log on Iran for startup status.';
      copy.hidden = !command;
      form.elements.license.value = '';
    } catch (err) {
      result.textContent = err.message;
    } finally {
      submit.disabled = false;
    }
  };
}