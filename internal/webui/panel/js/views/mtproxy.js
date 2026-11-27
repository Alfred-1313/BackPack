import * as api from '../api.js';
import { copyText, flashCopied } from '../lib/dom.js';
import { openScreen } from '../ui/screen.js';
import { oops } from '../ui/toast.js';

export function mtProxyView(ctx) {
  const name = ctx.params.name;
  openScreen('mtproxy', {
    bind: async (root, close) => {
      ctx.setTeardown(close);
      const field = id => root.querySelector('#' + id);
      const form = field('mtpCreate');
      const result = field('mtpResult');
      const status = field('mtpStatus');
      const select = field('mtpPort');
      const mode = field('mtpMode');
      const subtitle = field('mtpSubtitle');
      const setStatus = (text, error = false) => {
        status.textContent = text;
        status.classList.toggle('error', error);
      };
      mode.value = ctx.query?.get('mode') === 'web' ? 'web' : 'native';
      const isWeb = () => mode.value === 'web';
      const action = body => api.mtProxyAction(name, body, mode.value);

      const show = info => {
        field('mtpCommand').value = info.command;
        field('mtpLink').value = info.link;
        field('mtpRemove').value = info.remove;
        field('mtpFrontendRow').hidden = !info.frontend;
        field('mtpFrontend').value = info.frontend || '';
        field('mtpSponsorRow').hidden = isWeb();
        field('mtpRegister').value = info.registrationSecret || '';
        field('mtpSponsor').value = info.sponsor || '';
        form.hidden = true;
        result.hidden = false;
        subtitle.textContent = `${name} · ${info.engine || 'MTProxy'}`;
        setStatus(isWeb()
          ? `HTTPS ${info.address}:443 → TCP ${info.public} → ${info.bind}:${info.destination}`
          : `TCP ${info.address}:${info.public} → ${info.bind}:${info.destination}`);
      };
      const load = async () => {
        const requested = mode.value;
        const web = isWeb();
        form.hidden = result.hidden = true;
        setStatus('Reading tunnel settings…');
        const info = await api.mtProxySetup(name, requested);
        if (mode.value !== requested) return;
        subtitle.textContent = `${name} · Telemt ${info.version}`;
        field('mtpPath').textContent = web
          ? 'Telegram Desktop → HTTPS frontend → BackPack tunnel → Telemt WEB'
          : 'Telegram → forwarded TCP port → BackPack tunnel → MTProto on Kharej';
        field('mtpHostLabel').textContent = web ? 'Public HTTPS domain' : 'Public IP or hostname';
        field('mtpPortLabel').textContent = web ? 'Loopback TCP port on this server' : 'Forwarded TCP port';
        field('mtpNative').hidden = web;
        field('mtpDomain').required = !web;
        field('mtpWeb').hidden = !web;
        field('mtpWebIP').required = web;
        if (info.proxy) return show(info.proxy);
        if (!info.ports?.length) {
          setStatus(info.note || (web
            ? 'WEB needs a TCP mapping such as 127.0.0.1:18080=8443.'
            : 'Choose a public TCP mapping to the exit, such as 443=8443.'));
          return;
        }
        field('mtpHost').value = web ? '' : (info.host || '');
        field('mtpWebIP').value = web && /^(?:\d+\.){3}\d+$/.test(info.host) ? info.host : '';
        select.replaceChildren(...info.ports.map(p => new Option(
          `${p.public} → ${p.bind || '127.0.0.1'}:${p.destination}`, p.public,
        )));
        form.hidden = false;
        setStatus('');
      };
      const work = async (button, action) => {
        button.disabled = mode.disabled = true;
        try { await action(); }
        catch (e) { setStatus(e.message, true); }
        finally { button.disabled = mode.disabled = false; }
      };
      mode.addEventListener('change', () => load().catch(e => setStatus(e.message, true)));
      form.addEventListener('submit', event => {
        event.preventDefault();
        const button = form.querySelector('[type="submit"]');
        work(button, async () => {
          setStatus('Creating setup…');
          show(await action({
            action: 'create',
            host: field('mtpHost').value.trim(),
            port: Number(select.value),
            domain: isWeb() ? '' : field('mtpDomain').value.trim(),
            webIp: isWeb() ? field('mtpWebIP').value.trim() : '',
          }));
        });
      });
      field('mtpCheck').addEventListener('click', event => work(event.currentTarget, async () => {
        setStatus('Authenticating the proxy through the tunnel…');
        const { ms } = await action({ action: 'check' });
        setStatus(isWeb()
          ? `The WEB bridge authenticated over HTTPS in ${ms} ms. Open the link in compatible Telegram Desktop.`
          : `MTProxy authenticated through the tunnel in ${ms} ms. Open the link in Telegram to check the full connection.`);
      }));
      field('mtpSaveSponsor').addEventListener('click', event => work(event.currentTarget, async () => {
        show(await action({ action: 'sponsor', sponsor: field('mtpSponsor').value.trim() }));
        setStatus('Run the updated setup command on Kharej to apply the sponsor tag.');
      }));
      field('mtpClear').addEventListener('click', event => work(event.currentTarget, async () => {
        if (!window.confirm('Have you run the remove command on Kharej?')) return;
        await action({ action: 'clear' });
        await load();
      }));
      root.querySelectorAll('[data-copy]').forEach(btn => btn.addEventListener('click', async () => {
        flashCopied(btn, await copyText(field(btn.dataset.copy).value));
      }));
      try { await load(); } catch (e) { setStatus(e.message, true); }
    },
  }).catch(oops);
}
