import * as api from '../api.js';
import { copyText, el, flashCopied } from '../lib/dom.js';
import { openScreen } from '../ui/screen.js';
import { oops } from '../ui/toast.js';

export function mtProxyView(ctx) {
  const name = ctx.params.name;
  openScreen('mtproxy', {
    bind: async (root, close) => {
      const field = id => root.querySelector('#' + id);
      const form = field('mtpCreate');
      const result = field('mtpResult');
      const status = field('mtpStatus');
      const publicPort = field('mtpPort');
      const destination = field('mtpDestination');
      const portResult = field('mtpPortResult');
      const mode = field('mtpMode');
      const subtitle = field('mtpSubtitle');
      const setStatus = (text, error = false) => {
        status.textContent = text;
        status.classList.toggle('error', error);
        status.classList.remove('mtp-measured');
      };
      mode.value = ctx.query?.get('mode') === 'web' ? 'web' : 'native';
      const isWeb = () => mode.value === 'web';
      const action = (body, signal) => api.mtProxyAction(name, body, mode.value, signal);
      let settings, checkTimer, checkRequest, randomOnKharej = false;
      ctx.setTeardown(() => {
        clearTimeout(checkTimer);
        checkRequest?.abort();
        close();
      });

      const validPort = port => Number.isInteger(port) && port > 0 && port < 65536;
      const portValue = input => validPort(input.valueAsNumber) ? input.valueAsNumber : 0;
      const portError = (input, id, message) => {
        const error = field(id);
        error.textContent = message || '';
        error.hidden = !message;
        if (message) input.setAttribute('aria-invalid', 'true');
        else input.removeAttribute('aria-invalid');
      };
      const address = (bind, port) => (bind.includes(':') ? '[' + bind + ']' : bind) + ':' + port;
      function updatePortCommand() {
        if (!settings || settings.managed) return;
        const port = randomOnKharej ? 0 : portValue(destination);
        field('mtpPortCommand').value = 'sudo backpack mtproxy port ' + settings.bind + (port ? ' ' + port : '');
      }
      function manualResult() {
        const line = /^MTPORT (\S+) (FREE|BUSY)$/.exec(portResult.value.trim());
        if (!line) return false;
        const port = Number(line[1].split(':').at(-1));
        if (!validPort(port) || line[1] !== address(settings.bind, port)) return false;
        if (randomOnKharej && line[2] === 'FREE') {
          destination.value = port;
          randomOnKharej = false;
          updatePortCommand();
        }
        if (portValue(destination) !== port) return false;
        if (line[2] === 'BUSY') {
          portError(destination, 'mtpDestinationError', 'Port ' + port + ' (TCP) is already in use on Kharej.');
          return false;
        }
        return true;
      }
      const checkPorts = async () => {
        clearTimeout(checkTimer);
        checkRequest?.abort();
        if (!settings || !root.isConnected || form.hidden) return false;
        const thisCheck = new AbortController();
        checkRequest = thisCheck;
        portError(publicPort, 'mtpPublicError', '');
        portError(destination, 'mtpDestinationError', '');
        const confirmed = settings.managed || manualResult();
        const ports = { port: portValue(publicPort), destination: portValue(destination) };
        let errors;
        try { errors = await action({ action: 'check-ports', ...ports }, thisCheck.signal); }
        catch (e) { if (!thisCheck.signal.aborted) throw e; return false; }
        if (thisCheck.signal.aborted) return false;
        portError(publicPort, 'mtpPublicError', errors.public);
        if (settings.managed) portError(destination, 'mtpDestinationError', errors.destination);
        return !!(confirmed && ports.port && ports.destination && !errors.public && !errors.destination);
      };
      const scheduleCheck = () => {
        clearTimeout(checkTimer);
        checkRequest?.abort();
        checkTimer = setTimeout(() => checkPorts().catch(e => setStatus(e.message, true)), 350);
      };

      const show = info => {
        field('mtpCommand').value = info.command;
        field('mtpLink').value = info.link;
        field('mtpRemove').value = info.remove;
        field('mtpRemoval').hidden = true;
        field('mtpFrontendRow').hidden = !info.frontend;
        field('mtpFrontend').value = info.frontend || '';
        field('mtpSponsorRow').hidden = isWeb();
        field('mtpRegister').value = info.registrationSecret || '';
        field('mtpSponsor').value = info.sponsor || '';
        form.hidden = true;
        result.hidden = false;
        subtitle.textContent = name + ' \u00b7 ' + (info.engine || 'MTProxy');
        const ingress = isWeb() ? 'HTTPS ' + address(info.address, 443) + ' \u2192 TCP ' + info.public
          : 'TCP ' + address(info.address, info.public);
        setStatus(ingress + ' \u2192 ' + address(info.bind, info.destination) + ' on Kharej');
      };
      const load = async () => {
        const requested = mode.value;
        const web = isWeb();
        clearTimeout(checkTimer);
        checkRequest?.abort();
        form.hidden = result.hidden = true;
        setStatus('Reading tunnel settings\u2026');
        const info = await api.mtProxySetup(name, requested);
        if (mode.value !== requested) return;
        settings = info;
        randomOnKharej = false;
        portResult.value = '';
        portError(publicPort, 'mtpPublicError', '');
        portError(destination, 'mtpDestinationError', '');
        subtitle.textContent = name + ' \u00b7 Telemt ' + info.version;
        field('mtpPath').textContent = web
          ? 'Telegram Desktop connects to the HTTPS frontend on Iran. The tunnel carries the connection to the WEB proxy on Kharej.'
          : 'Telegram connects to the public port on Iran. The tunnel carries the connection to MTProxy on Kharej.';
        field('mtpHostLabel').textContent = web ? 'Public HTTPS domain' : 'Public IP or hostname';
        field('mtpPortLabel').textContent = web ? 'Loopback port on Iran' : 'Public port on Iran';
        field('mtpNative').hidden = web;
        field('mtpDomain').required = !web;
        field('mtpWeb').hidden = !web;
        field('mtpWebIP').required = web;
        if (info.proxy) return show(info.proxy);
        if (info.note) return setStatus(info.note);
        field('mtpHost').value = web ? '' : (info.host || '');
        field('mtpWebIP').value = web && /^(?:\d+\.){3}\d+$/.test(info.host) ? info.host : '';
        field('mtpExisting').replaceChildren(...(info.ports || []).map(p => new Option(
          'Forwards to ' + address(p.bind, p.destination), p.public,
        )));
        publicPort.value = '';
        destination.value = '';
        field('mtpManual').hidden = info.managed;
        updatePortCommand();
        form.hidden = false;
        setStatus('');
      };
      const work = async (button, task) => {
        button.disabled = mode.disabled = true;
        try { await task(); }
        catch (e) { setStatus(e.message, true); }
        finally { button.disabled = mode.disabled = false; }
      };
      mode.addEventListener('change', () => load().catch(e => setStatus(e.message, true)));
      publicPort.addEventListener('input', () => {
        const current = settings?.ports?.find(p => p.public === portValue(publicPort));
        if (current && !destination.value) {
          destination.value = current.destination;
          updatePortCommand();
        }
        scheduleCheck();
      });
      destination.addEventListener('input', () => {
        randomOnKharej = false;
        portResult.value = '';
        updatePortCommand();
        scheduleCheck();
      });
      portResult.addEventListener('input', scheduleCheck);
      field('mtpRandomPublic').addEventListener('click', event => work(event.currentTarget, async () => {
        const { port } = await action({ action: 'random-public', destination: portValue(destination) });
        publicPort.value = port;
        await checkPorts();
      }));
      field('mtpRandomDestination').addEventListener('click', event => work(event.currentTarget, async () => {
        if (settings.managed) {
          const { port } = await action({ action: 'random-destination' });
          destination.value = port;
          await checkPorts();
          return;
        }
        randomOnKharej = true;
        destination.value = '';
        portResult.value = '';
        portError(destination, 'mtpDestinationError', '');
        updatePortCommand();
        field('mtpPortCommand').focus();
        field('mtpPortCommand').select();
        setStatus('Run this command on Kharej to choose a free port, then paste its result.');
      }));
      form.addEventListener('submit', event => {
        event.preventDefault();
        const button = form.querySelector('[type="submit"]');
        work(button, async () => {
          setStatus('Checking ports\u2026');
          if (!await checkPorts()) {
            if (publicPort.getAttribute('aria-invalid')) publicPort.focus();
            else if (destination.getAttribute('aria-invalid')) destination.focus();
            else if (!settings.managed) portResult.focus();
            setStatus(settings.managed ? 'Check the forwarding ports.' : 'Check the destination on Kharej and paste the FREE result.', true);
            return;
          }
          setStatus('Creating setup\u2026');
          show(await action({
            action: 'create',
            host: field('mtpHost').value.trim(),
            port: portValue(publicPort),
            destination: portValue(destination),
            portResult: portResult.value.trim(),
            domain: isWeb() ? '' : field('mtpDomain').value.trim(),
            webIp: isWeb() ? field('mtpWebIP').value.trim() : '',
          }));
        });
      });
      field('mtpCheck').addEventListener('click', event => work(event.currentTarget, async () => {
        setStatus('Authenticating the proxy through the tunnel\u2026');
        const { ms } = await action({ action: 'check' });
        if (isWeb()) {
          setStatus('The WEB bridge authenticated over HTTPS in ' + ms + ' ms. Open the link in compatible Telegram Desktop.');
          return;
        }
        setStatus('MTProxy authenticated through the tunnel in ');
        status.classList.add('mtp-measured');
        status.append(el('span', { class: 'mtp-latency', text: ms + ' ms' }),
          '. Open the link in Telegram to check the full connection.');
      }));
      field('mtpSaveSponsor').addEventListener('click', event => work(event.currentTarget, async () => {
        show(await action({ action: 'sponsor', sponsor: field('mtpSponsor').value.trim() }));
        setStatus('Run the updated setup command on Kharej to apply the sponsor tag.');
      }));
      field('mtpDelete').addEventListener('click', event => work(event.currentTarget, async () => {
        if (!settings.managed) {
          field('mtpRemoval').hidden = false;
          field('mtpRemove').focus();
          return;
        }
        if (!window.confirm('Delete this MTProxy from Kharej?')) return;
        await action({ action: 'remove' });
        await load();
        setStatus('Proxy removed.');
      }));
      field('mtpCancelRemove').addEventListener('click', () => { field('mtpRemoval').hidden = true; });
      field('mtpClear').addEventListener('click', event => work(event.currentTarget, async () => {
        if (!window.confirm('Have you run the remove command on Kharej?')) return;
        await action({ action: 'remove', confirmed: true });
        await load();
        setStatus('Proxy removed.');
      }));
      root.querySelectorAll('[data-copy]').forEach(btn => btn.addEventListener('click', async () => {
        flashCopied(btn, await copyText(field(btn.dataset.copy).value));
      }));
      try { await load(); } catch (e) { setStatus(e.message, true); }
    },
  }).catch(oops);
}
