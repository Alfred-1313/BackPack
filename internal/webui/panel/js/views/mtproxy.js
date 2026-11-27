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
      const subtitle = field('mtpSubtitle');
      const setStatus = (text, error = false) => {
        status.textContent = text;
        status.classList.toggle('error', error);
      };
      subtitle.textContent = `${name} · Fake TLS (ee) · Trial`;

      const show = info => {
        field('mtpCommand').value = info.command;
        field('mtpLink').value = info.link;
        field('mtpRemove').value = info.remove;
        form.hidden = true;
        result.hidden = false;
        setStatus(`TCP ${info.address}:${info.public} → Kharej loopback :${info.destination}`);
      };
      const load = async () => {
        form.hidden = result.hidden = true;
        setStatus('Reading tunnel settings…');
        const info = await api.mtProxySetup(name);
        subtitle.textContent = `${name} · Fake TLS (ee) · mtg ${info.version} · Trial`;
        if (info.proxy) return show(info.proxy);
        if (!info.ports?.length) {
          setStatus(info.note || 'Choose a tunnel with a TCP port mapped to the exit server’s loopback, such as 443=127.0.0.1:8443.');
          return;
        }
        field('mtpHost').value = info.host || '';
        select.replaceChildren(...info.ports.map(p => new Option(
          `${p.public} → 127.0.0.1:${p.destination}`, p.public,
        )));
        form.hidden = false;
        setStatus('');
      };
      const work = async (button, action) => {
        button.disabled = true;
        try { await action(); }
        catch (e) { setStatus(e.message, true); }
        finally { button.disabled = false; }
      };
      form.addEventListener('submit', event => {
        event.preventDefault();
        const button = form.querySelector('[type="submit"]');
        work(button, async () => {
          setStatus('Creating setup…');
          show(await api.mtProxyAction(name, {
            action: 'create',
            host: field('mtpHost').value.trim(),
            port: Number(select.value),
            domain: field('mtpDomain').value.trim(),
          }));
        });
      });
      field('mtpCheck').addEventListener('click', event => work(event.currentTarget, async () => {
        setStatus('Authenticating MTProxy through the forwarded port…');
        const { ms } = await api.mtProxyAction(name, { action: 'check' });
        setStatus(`MTProxy authenticated through the tunnel in ${ms} ms. Open the link in Telegram to check the full connection.`);
      }));
      field('mtpClear').addEventListener('click', event => work(event.currentTarget, async () => {
        if (!window.confirm('Have you run the remove command on Kharej?')) return;
        await api.mtProxyAction(name, { action: 'clear' });
        await load();
      }));
      root.querySelectorAll('[data-copy]').forEach(btn => btn.addEventListener('click', async () => {
        flashCopied(btn, await copyText(field(btn.dataset.copy).value));
      }));
      try { await load(); } catch (e) { setStatus(e.message, true); }
    },
  }).catch(oops);
}
