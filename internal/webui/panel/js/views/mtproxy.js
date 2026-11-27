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
      const testProxy = field('mtpCheck');
      const testStatus = field('mtpTestStatus');
      const submit = field('mtpSubmit');
      const savedActions = field('mtpSavedActions');
      const newSetup = field('mtpNew');
      const removal = field('mtpRemoval');
      const publicPort = field('mtpPort');
      const destination = field('mtpDestination');
      const mode = field('mtpMode');
      const subtitle = field('mtpSubtitle');
      const label = field('mtpLabel');
      const limit = field('mtpLimit');
      const limitUnit = field('mtpLimitUnit');
      const setStatus = (text, error = false, target = status) => {
        target.textContent = text;
        target.classList.toggle('error', error);
        target.classList.remove('mtp-measured');
      };
      mode.value = ctx.query?.get('mode') === 'web' ? 'web' : 'native';
      const isWeb = () => mode.value === 'web';
      const action = (body, signal) => api.mtProxyAction(name, body, mode.value, signal);
      let settings, proxy, replacement, checkTimer, checkRequest, sponsor = '';
      let loading = 0, busy = false;
      ctx.setTeardown(() => {
        clearTimeout(checkTimer);
        checkRequest?.abort();
        close();
      });

      const validPort = port => Number.isInteger(port) && port > 0 && port < 65536;
      const portValue = input => {
        const { value } = input;
        const port = Number(value);
        return /^[0-9]{1,5}$/.test(value) && validPort(port) ? port : 0;
      };
      const portError = (input, id, message) => {
        const error = field(id);
        error.textContent = message || '';
        error.hidden = !message;
        if (message) input.setAttribute('aria-invalid', 'true');
        else input.removeAttribute('aria-invalid');
      };
      const validatePortField = (input, id) => {
        const error = input.value && !portValue(input) ? 'Enter a port from 1 to 65535.' : '';
        input.setCustomValidity(error);
        portError(input, id, error);
        return !error;
      };
      const address = (bind, port) => (bind.includes(':') ? '[' + bind + ']' : bind) + ':' + port;
      const destinationHint = message => {
        field('mtpDestinationHint').textContent = message || (settings.managed
          ? 'The proxy listens on this port on Kharej.'
          : 'The setup command checks this port on Kharej.');
      };
      const checkPorts = async (skipDestinationCheck = false) => {
        clearTimeout(checkTimer);
        checkRequest?.abort();
        if (!settings || !root.isConnected || form.hidden) return false;
        const validPublic = validatePortField(publicPort, 'mtpPublicError');
        const validDestination = validatePortField(destination, 'mtpDestinationError');
        if (!validPublic || !validDestination) return false;
        destinationHint();
        const thisCheck = new AbortController();
        checkRequest = thisCheck;
        const ports = { port: portValue(publicPort), destination: portValue(destination) };
        let errors;
        try { errors = await action({ action: 'check-ports', ...ports, skipDestinationCheck }, thisCheck.signal); }
        catch (e) { if (!thisCheck.signal.aborted) throw e; return false; }
        if (thisCheck.signal.aborted) return false;
        portError(publicPort, 'mtpPublicError', errors.public);
        portError(destination, 'mtpDestinationError', errors.destination);
        destinationHint(errors.destinationHint);
        return !!(ports.port && ports.destination && !errors.public && !errors.destination);
      };
      const scheduleCheck = () => {
        clearTimeout(checkTimer);
        checkRequest?.abort();
        checkTimer = setTimeout(() => checkPorts().catch(e => setStatus(e.message, true)), 350);
      };

      const savedFormValues = info => ({
        host: info.address, domain: info.domain, webIp: info.webIp, sponsor: info.sponsor,
        label: info.label, limit: info.limit,
      });
      const limitBytes = () => {
        const input = limit.value.trim();
        if (!input) return 0;
        const amount = Math.round(Number(input) * Number(limitUnit.value));
        return /^(?:\d+(?:\.\d*)?|\.\d+)$/.test(input) && Number.isSafeInteger(amount) && amount > 0 ? amount : null;
      };
      const validateLimit = () => {
        limit.setCustomValidity(limitBytes() == null ? 'Enter a valid positive amount or leave it blank for Unlimited.' : '');
        return limit.reportValidity();
      };
      limit.addEventListener('input', () => { limit.setCustomValidity(''); });
      limitUnit.addEventListener('change', () => { limit.setCustomValidity(''); });

      const show = (info, values = savedFormValues(info)) => {
        proxy = info;
        replacement = null;
        field('mtpCommand').value = info.command;
        field('mtpLink').value = info.link;
        field('mtpRemove').value = info.remove;
        field('mtpFrontendRow').hidden = !info.frontend;
        field('mtpFrontend').value = info.frontend || '';
        field('mtpSponsorRow').hidden = isWeb();
        field('mtpRegister').value = info.registrationSecret || '';
        field('mtpSponsor').value = info.sponsor || '';
        openForm(values);
        subtitle.textContent = [name, info.label, info.engine || 'MTProxy'].filter(Boolean).join(' \u00b7 ');
        const ingress = isWeb() ? 'HTTPS ' + address(info.address, 443) + ' \u2192 TCP ' + info.public
          : 'TCP ' + address(info.address, info.public);
        setStatus(settings.note || ingress + ' \u2192 ' + address(info.bind, info.destination) + ' on Kharej');
      };
      const formValues = () => ({
        host: field('mtpHost').value.trim(),
        port: portValue(publicPort),
        destination: portValue(destination),
        domain: isWeb() ? '' : field('mtpDomain').value.trim(),
        webIp: isWeb() ? field('mtpWebIP').value.trim() : '',
        sponsor,
        label: label.value.trim(),
        limit: limitBytes(),
      });

      const openForm = (values = {}) => {
        const web = isWeb();
        result.hidden = savedActions.hidden = testProxy.hidden = testStatus.hidden = !proxy;
        setStatus('', false, testStatus);
        removal.hidden = true;
        field('mtpHost').value = values.host ?? (web ? '' : settings.host || '');
        field('mtpDomain').value = values.domain || '';
        field('mtpWebIP').value = values.webIp ?? (web && /^(?:\d+\.){3}\d+$/.test(settings.host) ? settings.host : '');
        field('mtpExisting').replaceChildren(...(settings.ports || []).map(p => new Option(
          'Forwards to ' + address(p.bind, p.destination), p.public,
        )));
        label.value = values.label || '';
        label.placeholder = web ? 'MTProxy WEB' : 'MTProxy';
        const scale = values.limit && values.limit % (1024 ** 4) === 0 ? 1024 ** 4 : 1024 ** 3;
        limitUnit.value = scale;
        limit.value = values.limit ? +(values.limit / scale).toFixed(12) : '';
        limit.setCustomValidity('');
        publicPort.value = values.port || '';
        destination.value = values.destination || '';
        sponsor = values.sponsor || '';
        validatePortField(publicPort, 'mtpPublicError');
        validatePortField(destination, 'mtpDestinationError');
        destinationHint();
        field('mtpReplaceNotice').hidden = !proxy || !!settings.note;
        form.hidden = false;
        submit.hidden = newSetup.hidden = !!settings.note;
        submit.textContent = proxy ? 'Replace proxy' : 'Create proxy';
        setStatus('');
      };

      const load = async values => {
        const sequence = ++loading;
        const requested = mode.value;
        const web = isWeb();
        clearTimeout(checkTimer);
        checkRequest?.abort();
        form.hidden = result.hidden = removal.hidden = submit.hidden = savedActions.hidden = true;
        testProxy.hidden = testStatus.hidden = true;
        setStatus('Reading tunnel settings\u2026');
        const info = await api.mtProxySetup(name, requested);
        if (sequence !== loading || !root.isConnected) return;
        settings = info;
        proxy = null;
        replacement = null;
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
        openForm(values);
      };
      const work = async (button, task, target = status) => {
        if (busy) return;
        busy = true;
        button.disabled = mode.disabled = true;
        try { await task(); }
        catch (e) { setStatus(e.message, true, target); }
        finally { busy = button.disabled = mode.disabled = false; }
      };
      const openRemoval = values => {
        replacement = values;
        form.hidden = result.hidden = submit.hidden = savedActions.hidden = true;
        testProxy.hidden = testStatus.hidden = true;
        removal.hidden = false;
        field('mtpRemoveTitle').textContent = replacement ? 'Remove the current proxy to change ports' : 'Delete proxy';
        field('mtpRemoteRemoval').hidden = settings.managed;
        field('mtpDiscardRow').hidden = settings.managed;
        field('mtpClear').textContent = settings.managed ? 'Confirm deletion' : 'I ran the remove command';
        setStatus(settings.managed ? 'Confirm to remove the proxy from Kharej and clear its setup here.'
          : 'Run the command on Kharej, then confirm here.');
        (settings.managed ? field('mtpClear') : field('mtpRemove')).focus();
        removal.scrollIntoView({ block: 'nearest' });
      };
      const remove = (button, discard = false) => work(button, async () => {
        const replacing = !!replacement;
        const values = replacement ?? savedFormValues(proxy);
        setStatus('Removing the saved setup\u2026');
        await action({ action: discard ? 'clear' : 'remove', id: proxy.id, confirmed: true });
        await load(values);
        publicPort.focus();
        setStatus(discard ? 'Saved setup discarded. You can create a proxy.'
          : replacing ? 'Previous proxy removed. Check the new ports, then create the proxy.' : 'Proxy removed. You can create a new one.');
      });
      mode.addEventListener('change', () => load().catch(e => setStatus(e.message, true)));
      publicPort.addEventListener('input', () => {
        validatePortField(publicPort, 'mtpPublicError');
        scheduleCheck();
      });
      destination.addEventListener('input', () => {
        validatePortField(destination, 'mtpDestinationError');
        scheduleCheck();
      });
      field('mtpRandomPublic').addEventListener('click', event => work(event.currentTarget, async () => {
        const { port } = await action({ action: 'random-public', destination: portValue(destination) });
        publicPort.value = port;
        await checkPorts();
      }));
      field('mtpRandomDestination').addEventListener('click', event => work(event.currentTarget, async () => {
        const { port, checked } = await action({ action: 'random-destination' });
        destination.value = port;
        await checkPorts(true);
        if (!checked && portValue(destination) === port) {
          destinationHint('The setup command will check this port on Kharej.');
        }
      }));
      form.addEventListener('submit', event => {
        event.preventDefault();
        if (!validateLimit()) return;
        work(submit, async () => {
          setStatus('Checking ports\u2026');
          if (!await checkPorts()) {
            if (publicPort.getAttribute('aria-invalid')) publicPort.focus();
            else if (destination.getAttribute('aria-invalid')) destination.focus();
            setStatus('Check the forwarding ports.', true);
            return;
          }
          if (proxy) {
            openRemoval(formValues());
            return;
          }
          setStatus('Creating setup\u2026');
          try {
            show(await action({ action: 'create', ...formValues() }));
          } catch (err) {
            if (err.message.includes('is already in use on Kharej')) {
              portError(destination, 'mtpDestinationError', err.message);
              destination.focus();
            }
            throw err;
          }
          field('mtpCommand').focus();
        });
      });
      testProxy.addEventListener('click', () => work(testProxy, async () => {
        setStatus('Authenticating the proxy through the tunnel\u2026', false, testStatus);
        const { ms } = await action({ action: 'check' });
        if (isWeb()) {
          setStatus('The WEB session and WebSocket authenticated over HTTPS in ' + ms + ' ms. Open the link in compatible Telegram Desktop.', false, testStatus);
          return;
        }
        setStatus('MTProxy authenticated through the tunnel in ', false, testStatus);
        testStatus.classList.add('mtp-measured');
        testStatus.append(el('span', { class: 'mtp-latency', text: ms + ' ms' }),
          '. Open the link in Telegram to check the full connection.');
      }, testStatus));
      field('mtpSaveSponsor').addEventListener('click', event => work(event.currentTarget, async () => {
        show(await action({ action: 'sponsor', sponsor: field('mtpSponsor').value.trim() }));
        setStatus('Run the updated setup command on Kharej to apply the sponsor tag.');
      }));
      newSetup.addEventListener('click', () => {
        openForm(savedFormValues(proxy));
        publicPort.focus();
      });
      field('mtpDelete').addEventListener('click', () => openRemoval(null));
      field('mtpCancelRemove').addEventListener('click', () => {
        show(proxy, replacement || undefined);
      });
      field('mtpClear').addEventListener('click', event => remove(event.currentTarget));
      field('mtpDiscard').addEventListener('click', event => remove(event.currentTarget, true));
      root.querySelectorAll('[data-copy]').forEach(btn => btn.addEventListener('click', async () => {
        flashCopied(btn, await copyText(field(btn.dataset.copy).value));
      }));
      try { await load(); } catch (e) { setStatus(e.message, true); }
    },
  }).catch(oops);
}
