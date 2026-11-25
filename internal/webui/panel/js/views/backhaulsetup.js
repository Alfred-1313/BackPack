import * as api from '../api.js';
import { setupLinkHTML, bindSetupLink } from '../ui/setuplink.js';

export function showBackhaulSetup(panel, mode, label) {
  const form = document.createElement('form');
  form.innerHTML = `<h3></h3>
    <label>Tunnel name<input name="name" required maxlength="40" autocomplete="off"></label>
    <label>Tunnel port on Iran
      <input name="port" type="number" min="1" max="65535" required value="8443"></label>
    <label>Forwarded TCP ports (optional)<input name="ports" autocomplete="off" placeholder="443, 2053=8443"></label>
    <label data-address hidden>Iran IP or hostname<input name="iranHost" autocomplete="off"></label>
    <button type="submit" class="nb solid2">Create tunnel</button>
    <div aria-live="polite"></div>`;
  form.querySelector('h3').textContent = label;
  panel.append(form);

  const result = form.querySelector('[aria-live]');
  const submit = form.querySelector('[type="submit"]');
  bindSetupLink(result);
  form.onsubmit = async ev => {
    ev.preventDefault();
    const body = Object.fromEntries(new FormData(form));
    body.transport = mode;
    body.port = Number(body.port);
    submit.disabled = true;
    result.textContent = 'Creating the tunnel on Iran…';
    try {
      const response = await api.backhaulCreate(body);
      for (const field of form.elements) field.disabled = true;
      result.innerHTML = setupLinkHTML(response.setup);
      if (!response.active) result.prepend('Check the tunnel log on Iran for startup status. ');
    } catch (err) {
      result.textContent = err.message;
      if (err.message.includes('could not detect the Iran address')) {
        form.querySelector('[data-address]').hidden = false;
      }
      submit.disabled = false;
    }
  };
}
