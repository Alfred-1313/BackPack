import { transportDisplay, transportOption } from '../lib/transports.js';
/* Add a tunnel: pick the side, then the transport family, then the transport,
 * then the settings that side actually has.
 *
 * The families and the presets are served rather than written into the page, so
 * a transport added to the CLI menu appears here on its own and the two can
 * never describe different things.
 *
 * CLI: 1 Setup Iran, 2 Setup Kharej.
 */

import { $$, el, esc, dialogSubtitle, copyText, flashCopied } from '../lib/dom.js';
import { isUp } from '../lib/tstate.js';
import { NUMERIC } from '../lib/numeric.js';
import * as api from '../api.js';
import { setupLinkHTML, bindSetupLink } from '../ui/setuplink.js';
import * as store from '../store.js';
import { openScreen } from '../ui/screen.js';
import { oops, toast } from '../ui/toast.js';
import { go } from '../router.js';

const forwardPortsHint = 'Users connect to these ports here. A bare port goes to the same port on the kharej; '
  + '443=8080 sends 443 to its 8080. Separate several with commas.';

export function addView(ctx, replaceName = '') {
  return openScreen('add', {
    pick: '.dlg',
    bind: async (root, close) => {
      let stopSetupStatus = () => {};
      ctx.setTeardown(() => { stopSetupStatus(); close(); });
      let replacement;
      try {
        replacement = replaceName ? await api.tunnelRebuildInfo(replaceName) : null;
      } catch (err) {
        close();
        throw err;
      }
      if (!root.isConnected) return;
      dialogSubtitle(root, store.get().stats, 'this server’s end — the other is built from its setup link');
      let opts = { families: [], presets: [] };
      try { opts = await api.tunnelOptions(); } catch (e) { oops(e); }

      const [omniChoices, backhaulChoices, daggerChoices, penguChoices] = await Promise.all([
        api.omniOptions().catch(e => { oops(e); return []; }),
        api.backhaulOptions().catch(e => { oops(e); return []; }),
        api.daggerOptions().catch(e => { oops(e); return []; }),
        api.penguOptions().catch(e => { oops(e); return []; }),
      ]);
      const omniEntries = direction => omniChoices.filter(item => item.direction === direction).map(item => {
        const value = 'omni-' + item.value;
        return [value, { ...item, value, mode: item.value }];
      });
      const daggerEntries = direction => daggerChoices
        .filter(item => item.direction === direction)
        .map(item => ['dagger-' + item.value, {
          ...item, dagger: true, mode: item.value, value: 'dagger-' + item.value, port: item.value !== 'tun',
        }]);
      const penguEntries = direction => penguChoices
        .filter(item => !item.ipx || direction === 'direct')
        .map(item => {
          const value = 'pengu-' + item.id;
          return [value, { ...item, value, mode: item.id, pengu: true, port: !item.ipx,
            forward: true, needsRoot: item.tun, setupFamilies: item.ipx ? ['ipv4'] : ['ipv4', 'ipv6'] }];
        });
      const matrixEntry = direction => {
        const value = 'matrix-' + direction;
        return [value, { value, mode: 'matrix', label: 'Matrix', port: true,
          forward: true, udp: true, needsRoot: true, setupFamilies: ['ipv4', 'ipv6'] }];
      };
      const managedReverse = new Map([
        ...backhaulChoices.filter(item => item.direction === 'reverse').map(item => ['native-' + item.value, {
          ...item, native: true, port: true, value: 'native-' + item.value, mode: item.value,
        }]),
        ...omniEntries('reverse'),
        ...daggerEntries('reverse'),
        ...penguEntries('reverse'),
        matrixEntry('reverse'),
        ['easytier-reverse', { value: 'easytier-reverse', mode: 'easytier', label: 'EasyTier',
          port: true, forward: true, udp: true, setupFamilies: ['ipv4', 'ipv6'],
          desc: 'Private EasyTier network. Needs root.' }],
      ]);
      const managedDirect = new Map([
        ...omniEntries('direct'),
        ...daggerEntries('direct'),
        ...penguEntries('direct'),
        ...backhaulChoices.filter(item => item.value === 'tun-ipx').map(item => ['backhaul-tun-ipx', {
          ...item, value: 'backhaul-tun-ipx', mode: item.value, port: false, secret: false,
          forward: true, needsRoot: true, setupFamilies: ['ipv4'],
        }]),
        matrixEntry('direct'),
        ['gost-direct', { value: 'gost-direct', mode: 'gost-direct', label: 'GOST 3',
          port: true, secret: true, forward: true }],
        ['gost-direct-v2', { value: 'gost-direct-v2', mode: 'gost-direct-v2', label: 'GOST 2',
          port: true, secret: true, forward: true }],
      ]);
      /* families */
      /* The family list, the variants and the presets are all painted from
         /api/tunnel/options below, once the shape is known. */

      /* Steps and the pick-one groups are the preview's own handlers, rebound
         in screen.js. What is chosen is remembered here, and the form follows:
         reverse and direct are two different shapes, and each side of a tunnel
         is asked for different things — the Iran side owns the forwarded ports,
         the kharej side owns the address that is dialled. */
      const chosen = { side: 'server', direction: 'reverse', transport: null,
                       carrier: 'pck', preset: null };
      const mtMode = el('select', { id: 'addMTPMode' }, [
        el('option', { value: '', text: 'Off' }),
        el('option', { value: 'native', text: 'MTProto · Fake TLS' }),
        el('option', { value: 'web', text: 'MTProto WEB' }),
      ]);
      const mtEntry = el('input', { id: 'addMTPEntry', type: 'text', inputmode: 'numeric' });
      const mtExit = el('input', { id: 'addMTPExit', type: 'text', inputmode: 'numeric' });
      const mtHint = el('div', { class: 'hint' });
      const mtPorts = el('div', { class: 'fgrid', hidden: true }, [
        el('div', { class: 'f3' }, [el('label', { for: mtEntry.id, text: 'Port on this server' }), mtEntry]),
        el('div', { class: 'f3' }, [el('label', { for: mtExit.id, text: 'Port on Kharej' }), mtExit]),
      ]);
      const mtOptional = el('div', { class: 'grp3', id: 'addMTPOptional' }, [
        el('div', { class: 'gl3', text: 'Telegram MTProxy' }),
        el('div', { class: 'fgrid' }, [
          el('div', { class: 'f3 full' }, [
            el('label', { for: mtMode.id, text: 'Proxy mode' }), mtMode, mtHint,
          ]),
        ]),
        mtPorts,
      ]);
      const reverseSetup = () => chosen.direction === 'reverse' && managedReverse.get(chosen.transport);
      const nativeMuxOptional = () => ['xtcpmux', 'xwsmux'].includes(reverseSetup()?.mode);
      const daggerUpgrade = () => ['dagger-http', 'dagger-https'].includes(reverseSetup()?.mode);
      const daggerRoute = () => daggerUpgrade() || reverseSetup()?.mode === 'dagger-xhttp';
      const directSetup = () => chosen.direction === 'direct' && managedDirect.get(chosen.carrier);
      function mtProxyModes() {
        if (chosen.direction === 'direct') {
          if (directSetup() && chosen.carrier !== 'easytier') return [];
          return chosen.carrier === 'pck' ? ['native', 'web'] : ['native'];
        }
        if (chosen.transport === 'easytier-reverse') return ['native'];
        const carrier = (chosen.transport || '').replace(/^native-/, 'native/');
        return (opts.mtProxyReverse || []).includes(carrier) ||
          ['stealth', 'naive', 'xhttp', 'reality'].includes(carrier) ? ['native', 'web'] : [];
      }
      function paintMTProxyOptional() {
        const modes = mtProxyModes();
        mtMode.disabled = !modes.length;
        for (const option of mtMode.options) option.disabled = !!option.value && !modes.includes(option.value);
        if (mtMode.value && !modes.includes(mtMode.value)) mtMode.value = '';
        mtPorts.hidden = !mtMode.value;
        mtHint.textContent = !modes.length
          ? 'This transport has no supported TCP forwarding rule for MTProxy.'
          : mtMode.value === 'web'
            ? 'Uses a loopback forwarding rule. The public HTTPS endpoint needs a domain and a certificate on port 443.'
            : modes.includes('web')
              ? 'Reserve a TCP port to set up MTProxy after the tunnel is created.'
              : 'This routed tunnel can forward native MTProto to the private Kharej address.';
      }
      mtMode.addEventListener('change', paintMTProxyOptional);
      const penguSetup = () => {
        const selected = reverseSetup() || directSetup();
        return selected?.pengu ? selected : null;
      };
      const backhaulTUN = () => {
        if (directSetup()?.mode === 'tun-ipx') return 'tun-ipx';
        if (reverseSetup()?.mode === 'tun-tcp') return 'tun-tcp';
        return '';
      };
      const backhaulProfile = el('select', { id: 'backhaulIPXProfile' },
        ['tcp', 'udp', 'icmp', 'gre', 'ipip', 'bip'].map(value =>
          el('option', { value, selected: value === 'icmp', text: value.toUpperCase() })));
      const backhaulProfileRow = el('div', { class: 'f3 full' },
        el('label', { for: backhaulProfile.id, text: 'IPX profile' }), backhaulProfile,
        el('div', { class: 'hint', text: 'Uses the official Backhaul Premium core on both servers.' }));
      const backhaulIranTUN = el('input', { id: 'backhaulIranTUN', type: 'text', placeholder: 'Automatically assigned' });
      const backhaulKharejTUN = el('input', { id: 'backhaulKharejTUN', type: 'text', placeholder: 'Automatically assigned' });
      const backhaulHealthPort = el('input', { id: 'backhaulHealthPort', type: 'number',
        min: 1, max: 65535, placeholder: 'Automatically assigned' });
      const backhaulHealthRow = el('div', { class: 'f3 full' },
        el('label', { for: backhaulHealthPort.id, text: 'Health check port' }), backhaulHealthPort);
      const backhaulTUNFields = el('div', { class: 'full', hidden: true },
        backhaulProfileRow,
        el('div', { class: 'f3 full' }, el('label', { for: backhaulIranTUN.id, text: 'Iran tunnel IPv4/CIDR' }), backhaulIranTUN),
        el('div', { class: 'f3 full' }, el('label', { for: backhaulKharejTUN.id, text: 'Kharej tunnel IPv4/CIDR' }), backhaulKharejTUN,
          el('div', { class: 'hint', text: 'Leave both blank to allocate a private subnet.' })),
        backhaulHealthRow);
      const matrixSelected = () => (reverseSetup() || directSetup())?.mode === 'matrix';
      const easyTierSelected = () => chosen.direction === 'direct'
        ? chosen.carrier === 'easytier' : reverseSetup()?.mode === 'easytier';
      const easyTierProtocol = el('select', { id: 'easyTierProtocol' },
        ['tcp', 'udp', 'ws', 'wss'].map(value => el('option', { value, text: value.toUpperCase() })));
      const easyTierProtocolRow = el('div', { class: 'f3 full', hidden: true },
        el('label', { for: 'easyTierProtocol', text: 'EasyTier transport' }), easyTierProtocol);
      const officialDagger = () => {
        const setup = reverseSetup() || directSetup();
        return setup?.dagger ? setup.mode : '';
      };
      const waterWallSelected = () => chosen.direction === 'direct' && chosen.carrier === 'waterwall-packet';
      const waterWallField = (id, label, min, max) => {
        const field = tuningField(id, label);
        field.input.min = min;
        field.input.max = max;
        return field;
      };
      const wwIranWorkers = waterWallField('waterWallIranWorkers', 'Iran workers', 0, 64);
      const wwPeerWorkers = waterWallField('waterWallPeerWorkers', 'Kharej workers', 0, 64);
      wwIranWorkers.input.placeholder = wwPeerWorkers.input.placeholder = '0 (automatic)';
      const waterWallProtocol = el('input', { id: 'waterWallProtocol', type: 'number', min: 1, max: 254, value: 17 });
      const waterWallProtocolRow = el('div', { class: 'f3' },
        el('label', { for: waterWallProtocol.id, text: 'Outer IP protocol number' }), waterWallProtocol,
        el('div', { class: 'hint' }, [
          el('a', { href: 'https://en.wikipedia.org/wiki/List_of_IP_protocol_numbers', target: '_blank',
            rel: 'noopener noreferrer', text: 'List of IP protocol numbers' }),
        ]));
      const waterWallIranIP = el('input', { id: 'waterWallIranIP', type: 'text', placeholder: 'Automatic', spellcheck: 'false' });
      const waterWallPeerIP = el('input', { id: 'waterWallPeerIP', type: 'text', placeholder: 'Automatic', spellcheck: 'false' });
      const waterWallRandom = el('button', { type: 'button', class: 'mini3 waterwall-random', text: 'Random',
        'aria-label': 'Choose a random private IPv4 subnet for both sides', 'aria-controls': waterWallIranIP.id + ' ' + waterWallPeerIP.id });
      const waterWallOptions = el('div', { class: 'fgrid waterwall-options', hidden: true }, [
        waterWallProtocolRow,
        el('div', { class: 'f3' }, el('label', { for: waterWallIranIP.id, text: 'Iran tunnel IPv4/CIDR' }), waterWallIranIP),
        el('div', { class: 'f3' }, el('label', { for: waterWallPeerIP.id, text: 'Kharej tunnel IPv4/CIDR' }), waterWallPeerIP),
        waterWallRandom,
        wwIranWorkers.row, wwPeerWorkers.row,
      ]);
      waterWallRandom.addEventListener('click', async () => {
        const before = [waterWallIranIP.value, waterWallPeerIP.value];
        waterWallRandom.disabled = true;
        try {
          const { localIp, peerIp } = await api.waterWallSuggestSubnet(before[0]);
          if (waterWallIranIP.value === before[0] && waterWallPeerIP.value === before[1]) {
            waterWallIranIP.value = localIp;
            waterWallPeerIP.value = peerIp + '/' + localIp.split('/')[1];
          }
        } catch (e) {
          oops(e);
        } finally {
          waterWallRandom.disabled = false;
        }
      });
      root.querySelector('[name="peerAddr"]').closest('.f3').after(waterWallOptions);
      const pairedDirect = () => chosen.side === 'server' && chosen.direction === 'direct' &&
        ['gretap', 'l2tpv3', 'easytier', 'waterwall-packet'].includes(chosen.carrier);
      const setupProfile = () => opts.setupPerformance?.[chosen.direction === 'direct' ? chosen.carrier : chosen.transport];
      const setupChoices = new Map();
      let setupGroups, lastSetupProfile, daggerFields, penguFields;
      const addressChoice = el('select', { id: 'setupAddressFamily' }, [
        el('option', { value: 'ipv4', text: 'IPv4', selected: true }),
        el('option', { value: 'ipv6', text: 'IPv6' }),
      ]);
      const addressHint = el('div', { class: 'hint' });
      const addressRow = el('div', { class: 'f3 full' },
        el('label', { for: 'setupAddressFamily', text: 'Tunnel IP family' }), addressChoice, addressHint);
      const automaticDirect = () => chosen.direction === 'direct' && chosen.carrier !== 'spoof';
      const setupFamilies = () => {
        const direct = chosen.direction === 'direct';
        const setup = direct ? directSetup() : reverseSetup();
        if (setup) return setup.setupFamilies || (setup.native ? ['ipv4', 'ipv6'] : ['ipv4']);
        return direct
          ? opts.directSetupFamilies?.[chosen.carrier] || ['ipv4']
          : opts.reverseSetupFamilies?.[chosen.transport] || ['ipv4', 'ipv6'];
      };
      const familyHint = () => {
        addressHint.textContent = addressChoice.value === 'ipv6' && officialDagger() === 'dc6'
          ? 'DC6 uses IPv6 for the tunnel. BackPack detects the IPv4 address used for the license separately.'
          : 'BackPack detects the address for the Setup command.';
      };
      let preferredFamily = 'ipv4';
      addressChoice.addEventListener('change', () => {
        preferredFamily = addressChoice.value;
        familyHint();
      });
      const randomGreKey = root.querySelector('#randomGreKey');
      const nativeAddress = el('details', { class: 'f3 full', hidden: true });
      nativeAddress.append(el('summary', { text: 'Use a custom Iran address' }),
        el('label', { text: 'Iran IP or hostname' }, [
          el('input', { name: 'iranHost', type: 'text', autocomplete: 'off', placeholder: 'Automatically detected' }),
        ]));

      const directArea = root.querySelector('.step3direct');
      const directChoices = directArea.querySelector('.trgrid').closest('.grp3');
      const directFields = directArea.querySelector('[name="name"]').closest('.grp3');
      const directPeer = directFields.querySelector('[data-when="server"] [name="peerAddr"]');
      const directPort = directFields.querySelector('[name="tunnelPort"]');
      const directToken = directFields.querySelector('[data-when="server-!gre"] [name="token"]');
      const directForward = directFields.querySelector('[name="ports"]');
      const directNameRow = directFields.querySelector('[name="name"]').closest('.f3');
      const reverseNameRow = root.querySelector('#aname').closest('.f3');
      const reversePort = root.querySelector('#aport');
      const reversePortHint = reversePort.closest('.f3').querySelector('.hint');
      const standardReversePortHint = reversePortHint?.textContent;
      const reverseForward = root.querySelector('#aports');
      const forwardErrors = new Map([reverseForward, directForward, mtEntry].map((input, i) => {
        const id = 'forward-port-error-' + i;
        const note = el('div', { id, class: 'forward-port-error', hidden: true, 'aria-live': 'polite' });
        input.closest('.f3').append(note);
        input.setAttribute('aria-describedby', [input.getAttribute('aria-describedby'), id].filter(Boolean).join(' '));
        return [input, note];
      }));
      let forwardCheckTimer, forwardCheckRequest;
      const matrixSubnet = el('input', { id: 'matrixSubnet', type: 'text', placeholder: 'Automatic or 10.203.42.0/24', spellcheck: 'false' });
      const randomMatrixSubnet = el('button', { type: 'button', class: 'mini3 rnd', text: 'Random', 'aria-label': 'Choose a random private network', 'aria-controls': matrixSubnet.id });
      randomMatrixSubnet.addEventListener('click', async () => {
        const previous = matrixSubnet.value;
        randomMatrixSubnet.disabled = true;
        try {
          const { subnet } = await api.matrixSuggestSubnet(previous);
          if (matrixSubnet.value === previous) matrixSubnet.value = subnet;
        } catch (e) { oops(e); }
        finally { randomMatrixSubnet.disabled = false; }
      });
      const matrixSubnetRow = el('div', { class: 'f3', hidden: true }, el('label', { for: 'matrixSubnet', text: 'Private network' }),
        el('div', { class: 'withb' }, matrixSubnet, randomMatrixSubnet));
      directNameRow.after(matrixSubnetRow);
      const tokenWrap = el('div', { class: 'withb' });
      const copyToken = el('button', { type: 'button', class: 'mini3', text: 'Copy' });
      directToken.before(tokenWrap);
      tokenWrap.append(directToken, copyToken);
      copyToken.addEventListener('click', async () => {
        if (directToken.value) flashCopied(copyToken, await copyText(directToken.value.trim()));
      });
      const forwardUDP = el('div', { class: 'tg3 full', hidden: true },
        el('div', { class: 'tx' }, el('b', { text: 'Forward UDP as well as TCP' })),
        el('div', { class: 'sw3', 'data-name': 'routedAcceptUdp', 'aria-label': 'Forward UDP as well as TCP' }, el('i')));
      directForward.closest('.f3').after(forwardUDP);
      const directPortValues = { native: '', routed: '', matrix: '' };
      let directPortMode = 'native';

      /* The preset the form is already showing counts as chosen.
       *
       * It started as null and was only filled when somebody pressed one of
       * the buttons — but one of them is marked `on` in the markup, so the
       * form opens with Balance visibly selected. Accepting what is on screen
       * therefore sent no preset at all, and the tunnel was written without
       * one: the config had no preset line, the edit dialog read back an empty
       * value, and its menu fell to whichever option the preview was drawn
       * with. What the operator saw when they made the tunnel and what they
       * saw when they edited it disagreed, and neither was wrong about the
       * other — nothing had been recorded either way.
       *
       * Read from the markup rather than hard-coded, so the default is
       * whichever button carries `on`, and it stays right if that changes. */
      /* Reverse and direct each have a row of presets, and both are in the
         page. The first `.rp.on` in the document is the reverse row's, so a
         direct tunnel read Balance from a row it could not see. Only the row
         in the shape on screen counts — a step being hidden is not the row
         being hidden, which is why steps are passed over. */
      const inShape = n => {
        for (let at = n; at && at !== root; at = at.parentElement) {
          if (at.hidden && !at.classList.contains('step')) return false;
        }
        return true;
      };
      const markedPreset = () => {
        const live = [...root.querySelectorAll('.rp')].filter(inShape);
        const b = live.find(x => x.classList.contains('on')) || live[0];
        return (b?.querySelector('.key2, .k3')?.textContent || b?.dataset.pre || '')
          .trim().toLowerCase() || null;
      };

      const show = (sel, on) => root.querySelectorAll(sel)
        .forEach(n => { n.hidden = !on; });

      /* What each transport actually has, taken from the predicates in
         internal/manage/config.go rather than guessed:
           isMux   tcpmux, wsmux, wssmux, kcp, xdi, spoof, pck  -> the mux_* knobs
           isKCP   kcp, xdi, spoof, pck                          -> the kcp_* knobs and FEC
           needsTLS wss, wssmux                                  -> a certificate
         and the two raw carriers own their own drawer. A field that does not
         belong to the chosen transport is not shown, because writing it would
         put a key in the config that transport never reads. */
      const isMux = t => ['tcpmux', 'wsmux', 'wssmux', 'kcp', 'xdi', 'spoof', 'pck'].includes(t);
      const isKCP = t => ['kcp', 'xdi', 'spoof', 'pck'].includes(t);
      const isManaged = t => ['naive', 'anytls', 'xhttp', 'reality', 'gost'].includes(t);
      const needsTLS = t => ['wss', 'wssmux'].includes(t);
      /* Both taken from internal/manage/config.go, because the server refuses
         these combinations by name and a form that offers them is a form that
         collects an answer only to have it rejected. */
      const isWS = t => ['ws', 'wss', 'wsmux', 'wssmux'].includes(t);
      const isDatagram = t => ['udp', 'udp-padder', 'kcp', 'xdi', 'quic', 'pck'].includes(t);

      /* dw-ft fine tune · dw-sp spoof · dw-pk packet carrier · dw-cn connection */
      const drawer = id => root.querySelector('#dw-' + id);

      function applyFields() {
        const t = chosen.transport || '';
        const on = (sel, yes) => root.querySelectorAll(sel).forEach(n => {
          const row = n.closest('.f3, .tg3, .fhead') || n;
          row.hidden = !yes;
        });
        on('[name^="tune.mux"]', isMux(t));
        on('[name^="tune.kcp"]', isKCP(t));
        on('[data-when="tls"]', needsTLS(t));

        /* The packet-carrier drawer only means anything on its own transport —
           on plain TCP it was offering settings nothing reads. The spoof drawer
           is not here at all any more: spoof is a direct carrier, so applyShape
           decides it. */
        const pk = drawer('pk');
        if (pk) { pk.hidden = t !== 'pck'; if (t !== 'pck') pk.classList.remove('open'); }


        /* Managed transports are built here on the Iran side only, so their
           note, and REALITY's cover, show only there. */
        const https = isManaged(t) && chosen.side === 'server';
        const hr = root.querySelector('#httpsRow');
        if (hr) {
          hr.hidden = !https;
          hr.querySelector('code').textContent = t === 'gost' || t === 'anytls' ? 'BP_HELPERS=' + t : 'BP_HELPERS=naive,xray';
        }
        const cr = root.querySelector('#coverRow');
        if (cr) cr.hidden = !(https && t === 'reality');
        const tn = root.querySelector('#tlsNameRow');
        if (tn) tn.hidden = !(https && t === 'xhttp');
      }

      /* The settings every transport shows, hidden where this one ignores them.
         The same rules the server applies (spec.SupportsProxyProtocol,
         FineTune.apply, managedFromPanel) and the CLI asks by: a switch that
         is drawn and then dropped on save is a setting that seems to work. Run
         after the side rules, which would otherwise show them again. */
      function transportRows() {
        const t = chosen.transport || '';
        const iran = chosen.side === 'server';
        const row = sel => root.querySelector(sel)?.closest('.f3, .tg3');
        const set = (sel, yes) => { const r = row(sel); if (r) r.hidden = !yes; };
        const proxyOK = ['tcp', 'tcpmux', 'kcp', 'wsmux', 'wssmux', 'stealth', 'quic', 'pck'].includes(t);
        set('[data-name="proxyProtocol"]', iran && proxyOK);
        // udp forwards UDP ports by nature; the managed helpers carry TCP only.
        set('#ft-acceptUDP', !['udp', 'udp-padder'].includes(t) && !isManaged(t));
        set('#ft-zeroCopy', t === 'tcp');
        set('#ft-mss', !isDatagram(t));
        // The pool is the dialling side's; Iran never opens one.
        set('#ft-connectionPool', !iran);
        set('#ft-aggressivePool', !iran);
        // What the setup link hands the kharej: an edge only means anything
        // to a websocket transport, and the managed helpers take no backups.
        set('[name="kharejEdge"]', isWS(t));
        set('[name="kharejBackups"]', !isManaged(t));
      }

      /* Throughput is offered only where it applies — presetSuitsTransport. */
      function applyPresets() {
        const t = chosen.transport || '';
        root.querySelectorAll('.rp').forEach(b => {
          const key = b.querySelector('.key2')?.textContent.trim();
          const hide = key === 'throughput' && t !== 'kcp';
          b.hidden = hide;
          if (hide && b.classList.contains('on')) {
            b.classList.remove('on');
            root.querySelector('.rp:not([hidden])')?.classList.add('on');
          }
        });
        /* Whatever is marked is what will be sent. The `on` class is what the
           operator can see, so it is the one source of truth here — and it is
           re-read after the list changes, because Throughput disappearing moves
           the selection and the payload has to follow the screen. */
        chosen.preset = markedPreset();
      }

      /* The direct carriers, painted from the server rather than written into
         the page.
         
         They were a fourth hardcoded list — after the CLI wizard's, the panel's
         options endpoint and the create path's — and the four drifted: a
         carrier added to the others was offered nowhere here, and the endpoint
         behind this screen refused what this screen did not know about. There
         is one list now and this reads it. */
      /* What /api/direct/options said: the presets with what each sets in the PCK
         engine, and the lists its fine-tune menus offer. */
      let directOpts = null;
      async function paintCarriers() {
        const grid = directChoices.querySelector('.trgrid');
        try { directOpts = await api.directOptions(); } catch (e) { return; }
        const carriers = [...(directOpts.carriers || []), ...managedDirect.values()];
        if (!carriers.length) return;
        grid.innerHTML = carriers.map((c, i) => {
          const needsRoot = c.needsRoot ? '<span class="root2">needs root</span>' : '';
          return `<button class="dc${i === 0 ? ' on' : ''}" data-car="${esc(c.value)}"
            data-fn="setCar" data-args="'${esc(c.value)}'" title="${esc(c.desc || '')}">
            <span class="tn2">${transportOption(c.value, c.label)}${needsRoot}</span></button>`;
        }).join('');
        chosen.carrier = carriers[0].value;
        applyShape();
        showPresetDefaults();
      }
      const carriersReady = paintCarriers();

      function paintTransports() {
        const list = root.querySelector('#trlist');
        if (!list) return;
        const entries = (opts.families || []).flatMap(family => family.entries)
          .filter(item => chosen.side === 'server' || !item.iranOnly)
          .concat(chosen.side === 'server' ? [...managedReverse.values()] : []);
        if (!entries.some(item => item.value === chosen.transport)) chosen.transport = entries[0]?.value || null;
        list.innerHTML = entries.map(item => {
          const root2 = /needs root/i.test(item.desc || '') ? '<span class="root2">needs root</span>' : '';
          return `<button class="tr${item.value === chosen.transport ? ' on' : ''}" data-fn="setTr" data-args="'${esc(item.value)}'">
            <span class="tn2">${transportOption(item.value, item.label)}${root2}</span></button>`;
        }).join('');
        applyFields();
        applyPresets();
      }

      /* The drawers' controls were drawings.
       *
       * Every switch in the advanced drawers is a <div class="sw3"> and every
       * dropdown a <div class="sel4"> — the preview drew them that way, and
       * nothing ever made them controls. They have no name, so nothing they
       * showed was ever submitted, and the dropdowns opened no menu at all
       * because there was none to open. Fourteen switches and nine menus, all
       * of them ornamental.
       *
       * The ids already say what each one is: ft-nodelay, pk-flags, sp-profile,
       * cn-simpleAuth — a drawer prefix and the field's own name. So the wiring
       * is derived rather than listed: a hidden input named after the field
       * carries the value, and the drawing in front of it drives that input.
       * Nothing here invents a setting; every name below exists in FineTune,
       * SpoofTune, PckTune or ConnTune.
       */
      const DRAWER_GROUP = { ft: 'tune', sp: 'spoof', pk: 'pck', cn: 'conn' };

      const nameOfControl = (id, explicit) => {
        // A control outside the drawers says what it sets outright: the naming
        // convention only holds where there is a drawer to take the prefix from.
        if (explicit) return explicit;
        const at = id.indexOf('-');
        if (at < 0) return '';
        const group = DRAWER_GROUP[id.slice(0, at)];
        const field = id.slice(at + 1);
        return group && field ? `${group}.${field}` : '';
      };

      /* What each menu can be set to. The lists the server sends are used where
         it sends one, so the panel never offers an interface the machine does
         not have or a profile the engine does not know. */
      function choicesFor(id) {
        const auto = { value: '', label: 'Automatic — let the kernel choose the route' };
        const ifaces = () => [auto, ...(opts.interfaces || []).map(v => ({ value: v, label: v }))];
        switch (id) {
          case 'ft-logLevel':
            return ['debug', 'info', 'warn', 'error'].map(v => ({ value: v, label: v }));
          case 'sp-profile':
            return (opts.spoofProfiles || []).map(v => ({ value: v, label: v }));
          case 'sp-uplink':
          case 'sp-downlink':
            return [{ value: '', label: 'Same as the packet profile' },
              ...(opts.spoofProfiles || []).map(v => ({ value: v, label: v }))];
          case 'pck-mode':
            return [{ value: '', label: 'From the preset' },
              ...(directOpts?.pckModes || []).map(v => ({ value: v, label: v }))];
          case 'pck-block':
            return [{ value: '', label: 'From the preset (aes)' },
              ...(directOpts?.pckBlocks || []).filter(v => v !== 'aes').map(v => ({ value: v, label: v }))];
          case 'pk-flags':
            return [{ value: '', label: 'Default — push+ack, what a connection carrying data sends' },
              ...(opts.pckFlags || []).map(v => ({ value: v, label: v }))];
          default:
            return id.endsWith('interface') || id.endsWith('Iface') ? ifaces() : [];
        }
      }

      function wireDrawerControls() {
        /* Plain inputs in the drawers had ids and no names either — sp-mtu,
           sp-portMin, sp-sockBuf and the rest. Same convention, same fix: the
           id says which field it is, so it gets that name. */
        root.querySelectorAll('input[id]:not([name])').forEach(i => {
          const name = nameOfControl(i.id, i.dataset.name);
          if (name) i.name = name;
        });

        root.querySelectorAll('.sw3[id], .sw3[data-name]').forEach(sw => {
          const name = nameOfControl(sw.id || '', sw.dataset.name);
          if (!name || sw.dataset.wired) return;
          sw.dataset.wired = '1';
          const input = el('input', { type: 'checkbox', name, hidden: true });
          input.checked = sw.classList.contains('on');
          input.dataset.drawn = '1';
          sw.after(input);
          sw.setAttribute('role', 'switch');
          sw.setAttribute('aria-checked', String(input.checked));
          sw.tabIndex = 0;
          const flip = () => {
            sw.classList.toggle('on');
            input.checked = sw.classList.contains('on');
            delete input.dataset.drawn;
            sw.setAttribute('aria-checked', String(input.checked));
            if (name === 'tune.acceptUDP' || name === 'routedAcceptUdp') scheduleForwardCheck();
          };
          sw.addEventListener('click', flip);
          sw.addEventListener('keydown', ev => {
            if (ev.key === ' ' || ev.key === 'Enter') { ev.preventDefault(); flip(); }
          });
        });

        root.querySelectorAll('.sel4[id], .sel4[data-name]').forEach(sel => {
          const name = nameOfControl(sel.id || '', sel.dataset.name);
          const choices = choicesFor(sel.id);
          if (!name || !choices.length || sel.dataset.wired) return;
          sel.dataset.wired = '1';

          const input = el('input', { type: 'text', name, hidden: true });
          // The label it was drawn with is the default, so an untouched menu
          // means what it appears to mean.
          const shown = sel.childNodes[0]?.textContent?.trim() || '';
          const start = choices.find(c => c.label === shown) || choices[0];
          input.value = start.value;
          input.dataset.drawn = '1';
          sel.after(input);
          sel.setAttribute('role', 'combobox');
          sel.tabIndex = 0;

          const menu = el('div', { class: 'sel4menu', hidden: true });
          choices.forEach(c => {
            const opt = el('button', { type: 'button', class: 'sel4opt', text: c.label });
            opt.addEventListener('click', ev => {
              ev.stopPropagation();
              input.value = c.value;
              delete input.dataset.drawn;
              sel.childNodes[0].textContent = c.label;
              close4();
            });
            menu.append(opt);
          });
          sel.append(menu);

          const close4 = () => { menu.hidden = true; sel.classList.remove('open4'); };
          const open4 = () => {
            // One at a time: two menus open at once is two answers to one
            // question, and the second click lands on whichever is on top.
            root.querySelectorAll('.sel4menu').forEach(m => { m.hidden = true; });
            root.querySelectorAll('.sel4.open4').forEach(x => x.classList.remove('open4'));
            menu.hidden = false;
            sel.classList.add('open4');
          };
          sel.addEventListener('click', ev => {
            if (ev.target.closest('.sel4opt')) return;
            menu.hidden ? open4() : close4();
          });
          sel.addEventListener('keydown', ev => {
            if (ev.key === ' ' || ev.key === 'Enter') { ev.preventDefault(); menu.hidden ? open4() : close4(); }
            if (ev.key === 'Escape') close4();
          });
        });

        // A click anywhere else closes whatever is open.
        root.addEventListener('click', ev => {
          if (ev.target.closest('.sel4')) return;
          root.querySelectorAll('.sel4menu').forEach(m => { m.hidden = true; });
          root.querySelectorAll('.sel4.open4').forEach(x => x.classList.remove('open4'));
        });
      }

      /* What a direct tunnel should start out as.
       *
       * /api/direct/defaults answers with a subnet nothing on this machine is
       * using, a free interface name and a preset. The route and the handler
       * were both registered; the wrapper that calls them was never written, so
       * the panel never asked. The two fields that endpoint exists for are the
       * two an operator is least able to guess — the tunnel's own /30 has to
       * avoid every subnet already on the box, and picking one by hand is how
       * you end up with a tunnel that comes up and blackholes the route it was
       * built for.
       *
       * Only empty fields are filled, and only once per side: this runs from
       * applyShape, which fires on every change, and overwriting what somebody
       * has typed because they clicked something else is worse than not
       * suggesting at all. */
      /* The fields this fills, and only these.
       *
       * The endpoint also answers with a preset and a tunnel port. The preset
       * is a row of buttons rather than a field, and the tunnel port exists on
       * both shapes of this form and already has a Random button beside it —
       * filling either from here would be reaching past what the endpoint is
       * for. The subnet is what it is for: a tunnel's own /30 has to avoid
       * every subnet already on the box, and picking one by hand is how you get
       * a tunnel that comes up and blackholes the route it was built for. */
      const SUGGESTED_FIELDS = ['localIp', 'peerIp'];

      const suggestedFor = {};
      let suggestion = null;

      /* Fetched once per side, applied every time the shape is drawn.
       *
       * Applying it on the fetch alone does not work, and the reason is worth
       * writing down: this bind rearranges the form into five steps after the
       * template loads, so the field that exists when the answer arrives is not
       * the field the operator ends up typing into. Re-applying is free — it
       * only ever writes into a field that is still empty — and it lands
       * whenever the field appears, in whatever order the rest of the bind
       * happens to run. */
      function applySuggestion() {
        if (!suggestion) return;
        for (const name of SUGGESTED_FIELDS) {
          const value = suggestion[name];
          if (!value) continue;
          for (const f of root.querySelectorAll(`[name="${name}"]`)) {
            if (!f.value) f.value = value;
          }
        }
        /* A free tunnel port too, but only on the direct form (the row marked !gre; the reverse
           form has a tunnel port of its own, which this is not the answer to). */
        if (suggestion.tunnelPort && directPortMode === 'native' && !directPort.value) {
          directPort.value = suggestion.tunnelPort;
        }
      }

      async function suggestDirect(side) {
        applySuggestion();
        if (suggestedFor[side]) return;
        suggestedFor[side] = true;
        try {
          suggestion = await api.directDefaults(side === 'server' ? 'iran' : 'kharej');
        } catch (e) {
          suggestedFor[side] = false;
          return;
        }
        applySuggestion();
      }

      function applyShape() {
        const direct = chosen.direction === 'direct';
        const routed = directSetup();
        const setup = reverseSetup();
        const reverseMatrix = setup?.mode === 'matrix';
        const reverseEasyTier = setup?.mode === 'easytier';
        const tunMode = backhaulTUN();
        const ipx = tunMode === 'tun-ipx';
        const tunAnchor = (direct ? directForward : reverseForward).closest('.f3');
        if (backhaulTUNFields.previousElementSibling !== tunAnchor) tunAnchor.after(backhaulTUNFields);
        backhaulTUNFields.hidden = !tunMode;
        backhaulProfileRow.hidden = backhaulHealthRow.hidden = !ipx;
        const isEasyTier = easyTierSelected();
        const isWaterWall = waterWallSelected();
        waterWallOptions.hidden = !isWaterWall;
        const isMatrix = matrixSelected();
        show('.step3rev', !direct);
        show('.step3direct', direct);
        /* The line above the carriers was the markup's, written for Reverse,
           and nothing changed it: a direct tunnel — GRE included — was set up
           under "Reverse — the kharej server dials in to Iran". */
        const note = root.querySelector('#dirnote');
        if (note) note.textContent = direct
          ? chosen.carrier === 'gretap'
            ? 'Direct: GRETAP carries Ethernet frames between two IPv4 endpoints.'
            : chosen.carrier === 'l2tpv3'
              ? 'Direct: L2TPv3 carries Ethernet over IP protocol 115.'
              : chosen.carrier === 'easytier'
                ? 'Direct: Iran connects to Kharej using an encrypted EasyTier network.'
                : officialDagger() === 'tun'
                  ? 'Direct: DaggerConnect pairs both IPv4 endpoints using the Setup command.'
                  : 'Direct: Iran connects to Kharej. Peer-to-peer carriers exchange packets in both directions.'
          : 'Reverse — the kharej dials in to Iran. Try this first.';
        if (note && isWaterWall) note.textContent = 'Direct: WaterWall exchanges raw IPv4 packets between the two servers.';
        if (note && penguSetup()?.ipx) note.textContent = 'PenguTunnel IPX exchanges packets between fixed IPv4 endpoints.';
        const gretap = chosen.carrier === 'gretap';
        root.querySelector('#greEncryptionNote').textContent = isWaterWall ? 'WaterWall Packet Tunnel traffic is unencrypted.' : gretap
          ? 'GRETAP traffic is unencrypted. Use WireGuard, TLS or SSH inside it when encryption is needed.'
          : 'GRE traffic is unencrypted. Use WireGuard, TLS or SSH inside it when encryption is needed.';
        root.querySelector('#directGreKeyHint').textContent = gretap
          ? 'Leave blank or enter 0 for no key. If you choose a key, the Setup command carries it to Kharej.'
          : 'Use the same key on both ends. GRE picks one on Iran if the field is blank.';
        randomGreKey.hidden = !direct || chosen.side !== 'server' || !['gre', 'ip6gre', 'gretap'].includes(chosen.carrier);

        /* "server" is the Iran side, "client" the kharej side — the words the
           create endpoints use. */
        show('[data-when="server"]', chosen.side === 'server');
        show('[data-when="client"]', chosen.side === 'client');
        /* A field that belongs to one carrier, or to one side of one carrier.
           These read as conditions joined by "-": "client-spoof" is the kharej
           side of the spoof carrier, "sni" is the sni carrier whichever side
           this is. Nothing evaluated them before, so a row marked for one
           carrier was shown on every direct tunnel, on both sides — including
           the one telling the operator it was required. */
        const matchesCarrier = carrier => carrier === 'gre'
          ? ['gre', 'ip6gre', 'gretap', 'l2tpv3', 'waterwall-packet'].includes(chosen.carrier)
          : chosen.carrier === carrier;
        root.querySelectorAll('[data-when]').forEach(n => {
          const when = n.dataset.when;
          if (when === 'server' || when === 'client' || when === 'tls') return;
          const row = n.closest('.f3, .tg3, .f, .row') || n;
          /* "!gre" is every carrier but that one: the rows a kernel GRE link
             has no use for — a tunnel port, a token, error correction. */
          const ok = when.split('-').every(part => {
            if (part === 'server' || part === 'client') return chosen.side === part;
            if (part.startsWith('!')) return !matchesCarrier(part.slice(1));
            return matchesCarrier(part);
          });
          row.hidden = !ok;
        });
        if (isWaterWall) root.querySelector('[name="greKey"]').closest('.f3').hidden = true;
        directPeer.placeholder = routed || isWaterWall ? 'Automatically detected' : '203.0.113.9';
        directPeer.closest('.f3').querySelector('.hint').textContent = routed || pairedDirect()
          ? 'Leave blank to detect the address from the Setup connection.'
          : 'The server this side dials out to.';
        const subnetAnchor = reverseMatrix ? reverseNameRow : directNameRow;
        if (matrixSubnetRow.previousElementSibling !== subnetAnchor) subnetAnchor.after(matrixSubnetRow);
        matrixSubnetRow.hidden = !isMatrix;
        const protocolAnchor = direct ? directPort : reversePort;
        const protocolPortRow = protocolAnchor.closest('.f3');
        if (easyTierProtocolRow.previousElementSibling !== protocolPortRow) protocolPortRow.after(easyTierProtocolRow);
        easyTierProtocolRow.hidden = !isEasyTier;
        const portMode = routed?.mode === 'matrix' ? 'matrix' : routed ? 'routed' : 'native';
        if (portMode !== directPortMode) {
          directPortValues[directPortMode] = directPort.value;
          directPort.value = directPortValues[portMode];
          directPortMode = portMode;
        }
        const gost = routed?.mode?.startsWith('gost-direct');
        directPort.placeholder = isEasyTier ? '2090' : isMatrix ? '2097' : gost ? '1–65535' : routed ? 'Automatic' : 'e.g. 9000';
        directPort.closest('.f3').querySelector('.hint').textContent = routed
          ? (isMatrix ? 'Tinc uses this port for TCP and UDP. Blank uses 2097.' :
            gost ? 'Enter the port GOST listens on in Kharej.' : 'Leave blank to allocate a tunnel port automatically.')
          : isEasyTier ? 'Kharej listens on this port. Blank uses 2090.'
            : 'Kharej binds it; Iran reaches it. On PCK nothing else may use this port on the kharej.';
        if (routed) {
          directPort.closest('.f3').hidden = !routed.port;
          directToken.closest('.f3').hidden = !routed.secret;
          directForward.closest('.f3').hidden = !routed.forward;
        }
        const forwardRow = directForward.closest('.f3');
        forwardRow.querySelector('label').textContent = ipx || isWaterWall ? 'Forwarded TCP ports' : gost ? 'Forwarded TCP port' : 'Forwarded ports';
        forwardRow.querySelector('.hint').textContent = gost
          ? 'Enter one TCP forward. For example, 443=8443 sends Iran port 443 to Kharej port 8443.'
          : ipx ? 'Optional. Forwards TCP to the Kharej tunnel address.'
            : isMatrix ? 'Optional. Enter local ports, or use a peer private address such as 443=10.203.42.1:8443.' : forwardPortsHint;
        directForward.placeholder = gost ? '443=8443' : routed ? '443, 2053=8443' : '2087';
        const udpAnchor = (reverseMatrix || reverseEasyTier ? reverseForward : directForward).closest('.f3');
        if (forwardUDP.previousElementSibling !== udpAnchor) udpAnchor.after(forwardUDP);
        forwardUDP.hidden = !routed?.udp && !reverseMatrix && !isEasyTier;
        if (direct && !routed) suggestDirect(chosen.side);
        if (pairedDirect()) {
          root.querySelectorAll('[name="greLocal"], [name="localIp"], [name="peerIp"]').forEach(input => {
            input.closest('.f3').hidden = true;
          });
        }
        /* A group every one of whose rows that rule hid is a heading over
           nothing — GRE's "Carrier tuning", whose two switches are both
           "!gre". It goes with them, and comes back with them. */
        root.querySelectorAll('.grp3:not([data-setup-performance])').forEach(g => {
          const rows = [...g.querySelectorAll(':scope > .fgrid > *')];
          if (rows.length) g.style.display = rows.every(r => r.hidden) ? 'none' : '';
        });
        /* Groups that were moved out of .step3rev / .step3direct carry the mode
           they belong to, because the container that used to decide it is no
           longer their parent. */
        root.querySelectorAll('[data-mode]').forEach(g => {
          const wrong = g.dataset.mode !== (direct ? 'dir' : 'rev');
          g.hidden = wrong ||
            (!!setup && (!g.closest('.step3rev') || g.classList.contains('dr2b'))) ||
            (!!routed && g !== directChoices && g !== directFields);
          // A drawer left open in the other mode would spring back open with
          // its own settings when the operator switched away and back.
          if (g.hidden) g.classList.remove('open');
        });
        paintSetupTuning();
        paintDaggerFields();
        paintPenguFields();
        paintAddressFamily();
        if (automaticDirect()) {
          directPeer.closest('.f3').hidden = true;
          const local = directFields.querySelector('[name="greLocal"]');
          if (local) local.closest('.f3').hidden = true;
        }
        reversePort.closest('.f3').hidden = !!setup && !setup.port;
        reversePort.placeholder = reverseEasyTier ? '2090' : reverseMatrix ? '2097' : setup ? '1–65535' : '4443 or 85.10.11.51:4443';
        if (reversePortHint) reversePortHint.textContent = reverseMatrix
          ? 'Tinc listens on Iran over TCP and UDP. Blank uses 2097.'
          : reverseEasyTier ? 'Iran listens on this port. Blank uses 2090.' : standardReversePortHint;
        reverseForward.closest('.f3').querySelector('label').textContent =
          setup && !setup.dagger && !setup.pengu && !reverseMatrix && !reverseEasyTier ? 'Forwarded TCP ports' : 'Forwarded ports';
        reverseForward.placeholder = setup?.pengu ? '443, 8443=443, udp:51820' : 'e.g. 443, 8443';
        reverseForward.closest('.f3').querySelector('.hint').textContent =
          setup?.pengu ? 'Enter TCP ports or use udp: for UDP. Example: 8443=443, udp:51820.'
            : chosen.transport === 'udp-padder' ? 'UDP only. Example: 51820=51821. The original Depadder can remove X bytes at the end of the payload.'
              : forwardPortsHint;
        if (routed?.pengu) {
          directForward.placeholder = '443, 8443=443, udp:51820';
          directForward.closest('.f3').querySelector('.hint').textContent =
            'Enter TCP ports or use udp: for UDP. Example: 8443=443, udp:51820.';
        }
        if (routed?.dagger) directFields.hidden = true;
        // The token and the far end's address are the panel's business now.
        root.querySelectorAll('.tokgone, .addrgone').forEach(n => { n.hidden = true; });
        paintMTProxyOptional();
        scheduleForwardCheck();
        if (!direct) transportRows();
        paintRail();
        if (root.dataset.staged) paintNav(Number(root.querySelector('.step[data-s]:not([hidden])')?.dataset.s));

        /* The spoof settings belong to the spoof carrier. They used to hang off
           a reverse transport that no longer exists, which left every one of
           them unreachable — the drawer was in a section where its condition
           could never be true. */
        const sp = drawer('sp');
        if (sp) {
          const wanted = direct && chosen.carrier === 'spoof';
          sp.hidden = !wanted;
          if (!wanted) sp.classList.remove('open');
        }

        /* PCK's fine-tuning belongs to PCK, on the same terms as the spoof
           drawer above: a drawer left open in another carrier's form would post
           pck_* keys that carrier never reads. */
        const pk = drawer('pck');
        if (pk) {
          const wanted = direct && chosen.carrier === 'pck';
          pk.hidden = !wanted;
          if (!wanted) pk.classList.remove('open');
        }

        /* The forged source cannot be learned from the traffic, so the kharej
           side of a spoof carrier has to be told where its peer is. */
        const spoofField = root.querySelector('[name="spoofPeerIp"]')?.closest('.f3, div');
        if (spoofField) spoofField.hidden = !(direct && chosen.carrier === 'spoof'
                                              && chosen.side === 'client');
        if (!direct) { applyFields(); applyPresets(); }
        /* Direct has no applyPresets, so without this an untouched direct
           form sent no preset and the server took its default: Turbo, under a
           row showing Balance. */
        else chosen.preset = markedPreset();
      }

      /* The last step is not a summary.
         A reverse tunnel is set up on Iran first and finished on kharej; a
         direct one waits on kharej and is finished from Iran. On the side that
         finishes it there is nothing left to copy anywhere — what you want to
         know is whether it came up. So that side watches it come up, and the
         side that still has work to do gets the handoff instead. */
      /* The last step is not a summary.
         A reverse tunnel is set up on Iran first and finished on kharej; a
         direct one waits on kharej and is finished from Iran. On the side that
         finishes it there is nothing left to carry anywhere — the only question
         is whether it came up, so that side watches it come up. The side that
         still has work to do gets the handoff instead. */
      function finish() {
        const steps = [...root.querySelectorAll('.step[data-s]')];
        const step = steps[steps.length - 1];
        if (!step) return;

        /* One shape, because there is only one way to make a tunnel here now.
         *
         * This step used to have two: a pane that watched the tunnel come up,
         * and a pane that handed the operator a setup link and four values to
         * type into a second panel on the other server. The second one is gone.
         * The panel writes both ends over SSH, so there is nothing to carry
         * anywhere and nothing to type twice — and a form that still offered to
         * would be offering a way to build half a tunnel. */
        const hand = step.querySelector('.handpane');
        const conn = step.querySelector('.connpane');
        if (hand) hand.remove();
        if (conn) conn.hidden = false;
        if (conn) runBuild(conn);
      }

      /* Building this end, said out loud.
       *
       * Two stages, each a fact from the answer rather than a timer: the config
       * written, and the service up. Then the setup link — the other end is
       * the operator's to build, and the line that does it is what this screen
       * owes them, the way the menu's wizard prints it. */
      let building = false;
      async function runBuild(conn) {
        if (!conn || building) return;
        building = true;
        const rows = [...conn.querySelectorAll('.sg')];
        const title = conn.querySelector('#connTitle');
        const sub = conn.querySelector('#connSub');
        const result = conn.querySelector('#connResult');
        const spin = conn.querySelector('.spin');
        const t0 = Date.now();

        rows.slice(2).forEach(r => r.remove());
        ['Writing the configuration', 'Starting it here'].forEach((lb, i) => {
          const tx = rows[i]?.querySelector('.tx6');
          if (tx) tx.textContent = lb;
          if (rows[i]) rows[i].hidden = false;
          rows[i]?.classList.remove('done', 'doing', 'failed');
        });
        if (title) title.textContent = 'Building…';
        if (sub) sub.textContent = 'This server’s end, from what you filled in.';

        const settle = (i, ok) => {
          if (!rows[i]) return;
          rows[i].classList.remove('doing');
          rows[i].classList.add(ok ? 'done' : 'failed');
          const d = rows[i].querySelector('.dur');
          if (d) d.textContent = ((Date.now() - t0) / 1000).toFixed(1) + 's';
        };
        const start = i => rows[i]?.classList.add('doing');
        const pause = ms => new Promise(r => setTimeout(r, ms));

        start(0);
        let out;
        try {
          const conflict = await checkForwardFields();
          if (!root.isConnected || conn.closest('.step').hidden) {
            building = false;
            return;
          }
          if (conflict) {
            building = false;
            const pane = conflict.closest('.step');
            const panes = paneList();
            panes.forEach(p => { p.hidden = p !== pane; });
            root.dispatchEvent(new CustomEvent('step', { detail: { at: panes.indexOf(pane), of: panes.length } }));
            conflict.focus();
            return;
          }
          out = await submitTunnel();
        } catch (e) {
          settle(0, false);
          spin?.setAttribute('hidden', '');
          if (title) title.textContent = replacement ? 'Tunnel could not be rebuilt' : 'Nothing was created';
          if (sub) sub.textContent = 'This server refused the settings.';
          if (result) {
            result.innerHTML = `<div class="doneline warn"><span class="tick">!</span><div>
              <b>${esc(e.message || 'The panel could not build the tunnel')}</b>
              <span>Go back and correct the settings, then try again.</span>
            </div></div>`;
          }
          building = false;
          return;
        }

        const { r, name } = out;
        settle(0, true);
        if (r.pending) {
          const label = rows[0]?.querySelector('.tx6');
          if (label) label.textContent = 'Setup command ready';
          if (rows[1]) rows[1].hidden = true;
          spin?.setAttribute('hidden', '');
          if (title) title.textContent = 'Waiting for Kharej';
          if (sub) sub.textContent = replacement
            ? 'Run the Setup command on Kharej to apply the replacement. The current tunnel runs until then.'
            : 'Run this Setup command on the other server.';
          if (result) {
            result.innerHTML = setupLinkHTML(r);
            bindSetupLink(result);
            if (mtMode.value) result.append(el('p', { class: 'hint', text: 'After Kharej joins, open MTProxy from the tunnel card.' }));
          }
          if (waterWallSelected() && !replacement) {
            let closed = false;
            let hasTunnel = false;
            const unsubscribe = store.subscribe(({ tunnels }) => {
              const tunnel = tunnels.find(t => t.name === (r.name || name));
              if (!tunnel) return;
              const connected = isUp(tunnel);
              if (title) title.textContent = connected ? 'Tunnel connected' : 'Tunnel created on Iran';
              if (sub) sub.textContent = connected ? 'Both ends are exchanging traffic.'
                : 'The local tunnel is ready. Waiting for a reply over WaterWall.';
              if (label) label.textContent = 'Local configuration created';
              if (!hasTunnel) {
                hasTunnel = true;
                api.tunnelLink(tunnel.name).then(info => {
                  if (closed || info.pending) return;
                  const setup = result?.querySelector('.sl');
                  if (setup) setup.outerHTML = setupLinkHTML(info);
                }).catch(() => {});
              }
            });
            stopSetupStatus = () => { closed = true; unsubscribe(); };
            store.loadTunnels();
          }
          building = false;
          return;
        }
        await pause(240);
        start(1); await pause(200);
        const up = r.active !== false;
        settle(1, up);
        spin?.setAttribute('hidden', '');
        store.refresh();

        if (title) title.textContent = replacement ? 'Tunnel rebuilt on Iran'
          : up ? 'Built on this server' : 'Created, not up yet';
        if (sub) sub.textContent = replacement ? 'Run the Setup command to switch the existing Kharej tunnel.'
          : up ? 'Now build the other end with the line below.'
          : 'Config written, service not started — its log says why.';
        if (result) {
          result.innerHTML = `<div class="sl-wait">Building the setup link…</div>`;
          bindSetupLink(result);
          try {
            result.innerHTML = setupLinkHTML(r.setup || await api.tunnelLink(name));
          } catch (e) {
            result.innerHTML = `<div class="doneline warn"><span class="tick">!</span><div>
              <b>${esc(e.message)}</b><span>Open the tunnel's menu → Setup link to try again.</span></div></div>`;
          }
        }
        if (result && mtMode.value) {
          const proxy = el('button', { type: 'button', class: 'nb', text: 'Set up MTProxy' });
          proxy.addEventListener('click', () => go(`/t/${encodeURIComponent(name)}/mtproxy?mode=${mtMode.value}`));
          result.append(proxy);
        }
        building = false;
      }

      /* The single-ended flow, unchanged: nothing is created here, the tunnel
         already exists, and this only waits to see it come up. */

      /* The numbered header moved with the step in the preview; it is wired to
         the same event the Back and Continue buttons raise. */
      root.addEventListener('step', ev => {
        stopSetupStatus();
        const { at, of } = ev.detail;
        if (skipEmpty(at)) return;
        lastStep = at;
        root.querySelectorAll('.steps .st2').forEach(x =>
          x.classList.toggle('on', Number(x.dataset.s) === at));
        const back = root.querySelector('#backb');
        if (back) back.disabled = at === 0;
        paintNav(at);
        if (at === of - 1) finish();
      });

      root.addEventListener('pick', ev => {
        const { fn, value, el } = ev.detail;
        if (fn === 'setSide') {
          chosen.side = value;
          paintTransports();
        }
        if (fn === 'mode3') chosen.direction = value === 'dir' ? 'direct' : 'reverse';
        if (fn === 'setTr') { chosen.transport = value; applyFields(); applyPresets(); }
        if (fn === 'setCar') chosen.carrier = value;
        if (fn === 'setPre') chosen.preset = (el.querySelector('.key2, .k3')?.textContent
          || el.textContent).trim().toLowerCase().split(/\s+/)[0];
        applyShape();
        if (fn === 'setPre' || fn === 'setTr' || fn === 'setSide' || fn === 'mode3' || fn === 'setCar') showPresetDefaults();
      });

      /* The Fine Tune drawer calls itself "preset defaults" and showed empty
         boxes: the endpoint that says what a preset produces was never asked.
         The numbers go in as placeholders rather than values, because a value
         would be posted, and a drawer that posts everything it displays is a
         drawer that overrides the preset it is describing. */
      async function showPresetDefaults() {
        let d = {};
        try {
          d = await api.tunnelDefaults({
            preset: chosen.preset || '',
            role: chosen.side || 'server',
            transport: chosen.transport || '',
          }) || {};
        } catch (e) { return; }
        root.querySelectorAll('input[name^="tune."]').forEach(inp => {
          const key = inp.name.slice('tune.'.length);
          const v = d[key];
          inp.placeholder = (v === undefined || v === null || v === '') ? '' : String(v);
        });
        paintPresetSets(d);
        paintPckDefaults();
      }

      /* The fine-tune boxes show what the chosen preset sets as their grey text,
         and choosing another preset clears whatever was typed in them: a value
         left from the previous preset would hold the new one to the old numbers,
         which is not what choosing a preset means. A value is posted only while
         it is typed. */
      let pckPresetShown = null;
      function paintPckDefaults() {
        const p = chosen.preset || 'turbo';
        const o = (directOpts?.presets || []).find(x => x.value === p);
        if (!o || !o.pckConns) return;
        const set = (name, v) => root.querySelectorAll(`input[name="${name}"]`).forEach(i => {
          i.placeholder = v;
          if (pckPresetShown !== null && pckPresetShown !== p) i.value = '';
        });
        set('pckConns', o.pckConns); set('pckKcpSndWnd', o.pckSndWnd);
        set('pckKcpRcvWnd', o.pckRcvWnd); set('pckKcpMtu', o.pckMtu);
        if (pckPresetShown !== null && pckPresetShown !== p) {
          const mode = root.querySelector('input[name="pckKcpMode"]');
          const menu = root.querySelector('#pck-mode');
          if (mode && menu) {
            mode.value = ''; mode.dataset.drawn = '1';
            menu.childNodes[0].textContent = 'From the preset';
          }
        }
        pckPresetShown = p;
      }

      /* Under the preset cards: what the chosen one actually sets on this
         transport, read from the same answer the Fine Tune drawer shows. The
         step was three buttons and a screen of nothing; this is the thing an
         operator comparing them wants to see. */
      const DIRECT_SETS = {
        turbo: [['Socket buffer', '8 MB'], ['Queue', 'fq_codel'], ['Suits', 'most links']],
        balance: [['Socket buffer', 'smallest'], ['Queue', 'fq_codel'], ['Suits', 'small VPS']],
        aggressive: [['Socket buffer', '32 MB'], ['Queue', 'deep'], ['Suits', 'fast, bursty links']],
      };
      function paintPresetSets(d) {
        root.querySelectorAll('.rpgrid').forEach(grid => {
          if (grid.closest('[data-setup-performance]')) return;
          let box = grid.nextElementSibling;
          if (!box || !box.classList.contains('rp-sets')) {
            box = el('div', { class: 'rp-sets' });
            grid.after(box);
          }
          const direct = !!grid.closest('[data-mode="dir"], .step3direct');
          box.hidden = direct && ['gretap', 'l2tpv3', 'easytier', 'waterwall-packet'].includes(chosen.carrier);
          if (box.hidden) return;
          const p = chosen.preset || 'turbo';
          let cells;
          if (direct && chosen.carrier === 'pck') {
            /* PCK has no queue and no socket buffer: what a preset sets is the
               KCP engine's own parameters, which the server spells out. */
            const o = (directOpts?.presets || []).find(x => x.value === p) || {};
            cells = o.pckConns ? [['KCP sessions', o.pckConns], ['Speed mode', o.pckMode],
              ['Window', `${o.pckSndWnd} packets`], ['Buffers', o.pckBuf], ['Capture ring', o.pckCapture],
              ...(o.pckSuits ? [['Suits', o.pckSuits]] : [])] : [];
          } else if (direct) cells = DIRECT_SETS[p] || [];
          else {
            const n = (k, unit = '') => (d[k] ? [d[k] + unit] : []);
            cells = [
              ['Keepalive', ...n('keepAlive', ' s')], ['Heartbeat', ...n('heartbeat', ' s')],
              ['Channel', ...n('channelSize')],
              ...(isMux(chosen.transport) ? [['Mux streams', ...n('muxCon')]] : []),
              ...(isKCP(chosen.transport) ? [
                ['KCP window', ...(d.kcpSndWnd ? [`${d.kcpSndWnd}/${d.kcpRcvWnd}`] : [])],
                ['FEC', ...(d.kcpDataShards ? [`${d.kcpDataShards}+${d.kcpParityShards}`] : [])]] : []),
              ['No-delay', d.nodelay ? 'on' : 'off'],
            ].filter(c => c.length === 2);
          }
          const label = p[0].toUpperCase() + p.slice(1);
          box.innerHTML = `<div class="rp-sets-h"><b>What ${esc(label)} sets</b>
              <small>${direct ? (chosen.carrier === 'pck' ? 'on PCK' : 'on this direct tunnel') : `on ${esc((chosen.transport || '').toUpperCase())}`} · change any of it under ${direct && chosen.carrier === 'pck' ? 'PCK fine-tune' : 'Optional → Fine Tune'}</small></div>
            <div class="rp-sets-g">${cells.map(([k, v], i) =>
              `<div style="--d:${i * 35}ms"><span>${esc(k)}</span><b>${esc(String(v))}</b></div>`).join('')}</div>`;
        });
      }
      showPresetDefaults();

      /* Setup Iran and Setup Kharej are two entries in the CLI menu, so they are
         two links here; ?step= opens the wizard part-way, which is what a
         "now do the other side" link needs. */
      const q = ctx.query;
      if (q.get('side')) chosen.side = q.get('side') === 'kharej' ? 'client' : 'server';
      if (q.get('kind')) chosen.direction = q.get('kind') === 'direct' ? 'direct' : 'reverse';
      const markGroup = (fn, value) => {
        const b = [...root.querySelectorAll(`[data-fn="${fn}"]`)]
          .find(x => (x.dataset.args || '').includes(value));
        if (b) [...b.parentElement.children].forEach(x => x.classList.toggle('on', x === b));
      };
      markGroup('setSide', chosen.side);
      markGroup('mode3', chosen.direction === 'direct' ? 'dir' : 'rev');

      paintTransports();
      wireDrawerControls();
      applyShape();

      const stepTo = Number(q.get('step') || 0);
      if (stepTo) {
        const steps = [...root.querySelectorAll('.step[data-s]')];
        const at = Math.min(stepTo, steps.length - 1);
        steps.forEach((x, i) => { x.hidden = i !== at; });
        /* the same event Continue raises, so the header and the last step
           behave identically however the step was reached */
        setTimeout(() => root.dispatchEvent(
          new CustomEvent('step', { detail: { at, of: steps.length } })), 0);
      }

      /* Random asks the server for a port that is free on THIS machine, which
         is the right answer for exactly one field: the tunnel port, which this
         side binds.
       *
       * It used to be bound to every button labelled Random, and one of those
       * sat beside Forwarded ports — a field whose own hint says the value is
       * forwarded to the same port on the kharej machine. A port chosen for
       * being free here is, by construction, one nothing is listening on
       * there, so the button could only ever produce a tunnel that comes up,
       * reports a peer, and refuses every connection at the last hop. */
      [...root.querySelectorAll('.withb')]
        .filter(w => w.querySelector('input[name="tunnelPort"]'))
        .forEach(wrap => wrap.querySelectorAll('button').forEach(b => {
          if (!/^random$/i.test(b.textContent.trim())) return;
          b.addEventListener('click', async () => {
            try {
              const r = await api.tunnelSuggest();
              const field = wrap.querySelector('input[name="tunnelPort"]');
              if (field && r.port) {
                field.value = r.port;
                scheduleForwardCheck();
              }
            } catch (e) { oops(e); }
          });
        }));

      /* Forwarded ports get a Random of their own, asked for by name: a port
         free on this server, added to the list. It is a port for users to
         reach this server on, and the kharej hands it to the same port there —
         which the hint under the field now says in as many words, because the
         service on the kharej has to be listening on it. */
      root.querySelectorAll('input[name="ports"]').forEach(inp => {
        if (inp.closest('.withb')) return;
        const wrap = el('div', { class: 'withb' });
        inp.before(wrap);
        wrap.append(inp);
        const b = el('button', { type: 'button', class: 'mini3 rnd', text: 'Random' });
        wrap.append(b);
        b.addEventListener('click', async () => {
          try {
            const r = await api.tunnelSuggest();
            if (!r.port) return;
            if (inp === directForward && directSetup()?.mode?.startsWith('gost-direct')) {
              const target = inp.value.split('=', 2)[1]?.trim();
              inp.value = target ? r.port + '=' + target : String(r.port);
            } else {
              const have = inp.value.split(',').map(x => x.trim()).filter(Boolean);
              if (!have.includes(String(r.port))) have.push(String(r.port));
              inp.value = have.join(', ');
            }
            inp.dispatchEvent(new Event('input', { bubbles: true }));
          } catch (e) { oops(e); }
        });
        const hint = wrap.parentElement?.querySelector('.hint');
        if (hint) hint.textContent = forwardPortsHint;
      });

      [...root.querySelectorAll('button')]
        .filter(b => /show as a cli command/i.test(b.textContent.trim()))
        .forEach(b => b.addEventListener('click', async () => {
          const get = n => root.querySelector(`[name="${n}"], #${n}`)?.value?.trim() || '';
          const line = ['sudo backpack',
            chosen.direction === 'direct' ? 'direct' : 'reverse',
            chosen.side === 'server' ? '--iran' : '--kharej',
            chosen.transport ? '--transport ' + chosen.transport : '',
            get('aname') ? '--name ' + get('aname') : '',
          ].filter(Boolean).join(' ');
          try { await navigator.clipboard.writeText(line); toast('Command copied.'); }
          catch (e) { toast(line); }
        }));

      /* Neither the setup link nor the pane that pasted one back is here any
         more. Both existed for a second panel on the other server, typing the
         same values in again; the panel writes that end itself now. */

      /* A step with nothing in it is not a step.
       *
       * The five are the parts of a reverse tunnel. A direct one has no
       * connectivity drawer for the far end and no Optional group at all, so
       * two of the panes hold groups that are all hidden for it — and pressing
       * Continue landed on a blank screen with a Continue button, twice.
       *
       * Rather than build filler for them, the wizard skips what is empty and
       * says so in the rail: the steps that exist are the ones that have
       * something to ask. It is recomputed on every change because the answer
       * depends on the mode and the transport, not on the shape of the form.
       */
      const paneList = () => [...root.querySelectorAll('.step[data-s]')];

      function paneEmpty(pane, i, of) {
        // The last one is the result, which is not made of groups.
        if (i === of - 1) return false;
        if (pane.querySelector('.choices')) return false;
        return ![...pane.querySelectorAll('.grp3, .dr2b')].some(g => {
          const hidden = g.closest('[hidden]');
          return !hidden || hidden === pane;
        });
      }

      function paintRail() {
        if (!root.dataset.staged) return;
        const panes = paneList();
        const rail = root.querySelector('.steps');
        if (!rail) return;
        let n = 0;
        panes.forEach((pane, i) => {
          const entry = rail.querySelector(`.st2[data-s="${i}"]`);
          if (!entry) return;
          const skip = paneEmpty(pane, i, panes.length);
          entry.hidden = skip;
          const bar = entry.nextElementSibling;
          if (bar && bar.classList.contains('bar4')) bar.hidden = skip;
          if (!skip) {
            n += 1;
            const num = entry.querySelector('.n3');
            if (num) num.textContent = String(n);
          }
        });
      }

      /* Travelling on from a step that has nothing to show, in the direction
         the operator was already going. */
      let lastStep = 0;
      function skipEmpty(at) {
        const panes = paneList();
        if (!root.dataset.staged || !panes[at]) return false;
        if (!paneEmpty(panes[at], at, panes.length)) return false;
        const dir = at >= lastStep ? 1 : -1;
        const to = at + dir;
        if (to < 0 || to >= panes.length) return false;
        panes.forEach((x, i) => { x.hidden = i !== to; });
        root.dispatchEvent(new CustomEvent('step', { detail: { at: to, of: panes.length } }));
        return true;
      }

      /* The footer says what the next press does. On the step before the last
         one it is not "continue" — it is the moment the tunnel gets built, and a
         button that does something irreversible should say so. */
      function paintNav(at) {
        const next = root.querySelector('#nextb');
        const note = root.querySelector('#note4');
        if (!next || !root.dataset.staged) return;
        const last = root.querySelectorAll('.step[data-s]').length - 1;
        if (at === last) {
          next.textContent = 'Close';
          next.disabled = false;
          next.onclick = () => { close(); go('/'); };
        } else {
          const panes = paneList();
          const creates = panes.slice(at + 1, last).every((pane, i) => paneEmpty(pane, at + 1 + i, panes.length));
          next.textContent = creates ? (replacement ? 'Save changes' : 'Create the tunnel') : 'Continue';
          next.onclick = null;
        }
        if (note) note.textContent = at === last - 1
          ? 'This server’s end is written when you press this.' : '';
      }

      /* The wizard is held back until the fleet has answered.
       *
       * Whether there are managed servers decides how many steps there are and
       * what the first one is, and that answer arrives a moment after the
       * dialog opens. Showing the old first step and then rearranging it under
       * the operator is worse than showing nothing for that moment — they were
       * reading it. The hiding is in add.css, because the markup is on screen
       * before this runs.
       */
      const reveal = () => root.classList.add('ready');
      // Never left hidden by a request that hangs.
      setTimeout(reveal, 2500);

      /* One server: this one.
       *
       * The wizard used to need a managed server to write the far end on, and
       * refused to build anything without one. Servers is out of the panel, so
       * the form is what the CLI wizard is — Setup Iran or Setup Kharej, on the
       * machine in front of you — and the other end is built from the setup
       * link this screen hands over at the end. Six steps, in the order the
       * menu asks: which side, which kind, the tunnel, how fast, what else,
       * and what happened. */
      const SINGLE = ['Type', 'Tunnel', 'Performance', 'Optional', 'Done'];

      /* The preset cards: the three (four, on udp+kcp) profiles as cards that
         fill the row, each with its own mark and what it costs — they were
         three small buttons in an empty step. The button keeps its class, its
         data-pre and its .key2, because those are what the rest of the form
         reads. */
      const PRESET_ART = {
        balance: { icon: '<svg viewBox="0 0 24 24"><path d="M12 3v18"/><path d="M5 7h14"/><path d="M5 7l-3 7a3.5 3.5 0 006 0z"/><path d="M19 7l-3 7a3.5 3.5 0 006 0z"/><path d="M8 21h8"/></svg>',
          line: 'Light on CPU and memory', fits: 'Small or shared VPS · several tunnels on one box', load: 1 },
        turbo: { icon: '<svg viewBox="0 0 24 24"><path d="M13 2L4.5 13.5H11L10 22l8.5-11.5H12z"/></svg>',
          line: 'The tuned default', fits: 'Most Iran → abroad links', load: 2, rec: true },
        aggressive: { icon: '<svg viewBox="0 0 24 24"><path d="M12 22c4.4 0 7-2.9 7-6.8 0-3.6-2.4-6-4.2-8.2-.4 2.2-1.6 3.4-2.8 4C12.3 7.6 10.4 4.6 8 2c.2 3.5-3 6-3 11.2C5 18.9 7.6 22 12 22z"/><path d="M12 22c-1.7 0-3-1.3-3-3.2 0-1.8 1.4-2.9 3-4.8 1.6 1.9 3 3 3 4.8 0 1.9-1.3 3.2-3 3.2z"/></svg>',
          line: 'Maximum headroom', fits: 'Strong servers · gaming · fast links with bursts', load: 3 },
        throughput: { icon: '<svg viewBox="0 0 24 24"><path d="M4 18a8 8 0 1116 0"/><path d="M12 18l4.5-6"/><circle cx="12" cy="18" r="1.4"/></svg>',
          line: 'Bandwidth over steady ping', fits: 'udp + kcp + fec only', load: 3 },
      };
      function dressPresets() {
        root.querySelectorAll('.rp').forEach(b => {
          const v = (b.dataset.pre || b.querySelector('.key2')?.textContent || '').trim().toLowerCase();
          const a = PRESET_ART[v];
          if (!a || b.dataset.dressed) return;
          b.dataset.dressed = '1';
          const label = v[0].toUpperCase() + v.slice(1);
          b.classList.add('rp2');
          b.innerHTML = `<span class="rp-ic">${a.icon}</span>
            <span class="rp-tx"><b>${label}${a.rec ? '<em>recommended</em>' : ''}</b>
              <span class="rp-line">${a.line}</span><small>${a.fits}</small></span>
            <span class="rp-load" title="CPU and memory it takes">${[1, 2, 3].map(i => `<i class="${i <= a.load ? 'on' : ''}"></i>`).join('')}</span>
            <span class="key2" hidden>${v}</span>`;
        });
        root.querySelectorAll('.rp2').forEach(b => b.parentElement?.classList.add('rpgrid'));
      }

      function tuningField(id, label) {
        const input = el('input', { id, type: 'number', step: '1' });
        const title = el('label', { for: id, text: label });
        const row = el('div', { class: 'f3' }, [title, input]);
        return { row, title, input };
      }

      function tuningDrawer(title, rows, note = '') {
        const header = el('button', { type: 'button', class: 'h4b', 'aria-expanded': 'false' }, [
          title, el('span', { class: 'drawcount', text: note }),
        ]);
        header.insertAdjacentHTML('beforeend', '<svg class="cv2" viewBox="0 0 24 24"><path d="M6 9l6 6 6-6"/></svg>');
        const grid = el('div', { class: 'fgrid' }, rows);
        const box = el('div', { class: 'dr2b' }, [header, el('div', { class: 'b4b' }, grid)]);
        header.addEventListener('click', () => header.setAttribute('aria-expanded', String(box.classList.toggle('open'))));
        return box;
      }

      function tuningSwitch(title, hint) {
        const button = el('button', { type: 'button', class: 'sw3', role: 'switch', 'aria-checked': 'false', 'aria-label': title }, el('i'));
        button.addEventListener('click', () => button.setAttribute('aria-checked', String(button.classList.toggle('on'))));
        const row = el('div', { class: 'tg3 full' }, [el('div', { class: 'tx' }, [
          el('b', { text: title }), el('span', { text: hint }),
        ]), button]);
        return { row, checked: () => button.classList.contains('on') };
      }

      function addMuxOptional(opt) {
        const address = (name, id) => {
          const row = opt.querySelector(`[name="${name}"]`).closest('.f3').cloneNode(true);
          const input = row.querySelector('input');
          input.removeAttribute('name');
          input.id = id;
          row.querySelector('label').htmlFor = id;
          return { row, input };
        };
        const dial = address('kharejDial', 'nativeMuxDial');
        dial.row.querySelector('.hint').textContent = 'A hostname or an IP address. Empty uses this server’s detected address.';
        const backups = address('kharejBackups', 'nativeMuxBackups');
        const field = (id, name, min, max, placeholder) => {
          const f = tuningField(id, name);
          f.input.min = min;
          f.input.max = max;
          f.input.placeholder = placeholder;
          return f;
        };
        const heartbeat = field('nativeMuxHeartbeat', 'Heartbeat interval (seconds)', 1, 300, '5');
        const timeout = field('nativeMuxTimeout', 'Heartbeat timeout (seconds)', 1, 900, '16');
        timeout.row.append(el('div', { class: 'hint', text: 'Must be longer than the heartbeat interval.' }));
        const conns = field('nativeMuxMaxConnections', 'Maximum simultaneous connections', 0, 65535, '0');
        const bandwidth = field('nativeMuxBandwidth', 'Bandwidth limit (Mbit/s)', 0, 100000, '0');
        const fine = tuningDrawer('Fine Tune', [heartbeat.row, timeout.row], 'preset defaults');
        const limits = tuningDrawer('Connection & limits', [
          el('div', { class: 'fnote', text: 'Caps for the whole tunnel. 0 means no limit.' }), conns.row, bandwidth.row,
        ]);
        const ipv6 = tuningSwitch('Listen on IPv6 as well', 'Bind the v6 address too, where this server has one.');
        const proxy = tuningSwitch('PROXY protocol', 'Sends the real client IP. Turn on only if the forwarded service accepts PROXY protocol v2.');
        const group = el('div', { class: 'grp3', hidden: true, 'data-native-mux-optional': '' }, [
          el('div', { class: 'gl3', text: 'Optional' }),
          el('div', { class: 'fgrid kharejgrid' }, [dial.row, backups.row]),
          fine, limits, ipv6.row, proxy.row,
        ]);
        opt.append(group);
        return { group, dial, backups, heartbeat, timeout, conns, bandwidth, ipv6, proxy };
      }

      function addSetupTuning(tunnelFields, perf, opt) {
        const cards = el('div', { class: 'trgrid' });
        const summary = el('div', { class: 'rp-sets' });
        const performanceFields = el('div', { class: 'fgrid', hidden: true });
        const group = el('div', { class: 'grp3', hidden: true, 'data-setup-performance': '' }, [
          el('div', { class: 'gl3', text: 'Performance' }), cards, summary, performanceFields,
        ]);
        const value = tuningField('setupTuningValue', '');
        const wwIranMTU = waterWallField('waterWallIranMTU', 'Iran MTU (bytes)', 576, 1500);
        const wwPeerMTU = waterWallField('waterWallPeerMTU', 'Kharej MTU (bytes)', 576, 1500);
        const waterWallFields = el('div', { class: 'fgrid waterwall-options', hidden: true },
          wwIranMTU.row, wwPeerMTU.row);
        performanceFields.append(waterWallFields);
        const delay = tuningField('setupRestartSeconds', 'Restart delay after failure (seconds)');
        delay.input.min = 1;
        delay.input.max = 60;
        const mtu = tuningField('setupTunnelMTU', 'MTU (bytes)');
        mtu.input.min = 576;
        mtu.input.max = 1400;
        mtu.input.placeholder = '1400';
        const httpField = (id, label, placeholder, maxlength) => {
          const input = el('input', { id, type: 'text', placeholder, maxlength, autocomplete: 'off' });
          return { input, row: el('div', { class: 'f3', hidden: true }, [el('label', { for: id, text: label }), input]) };
        };
        const httpPath = httpField('nativeDaggerHTTPPath', 'HTTP path', '/tunnel', 256);
        const httpHost = httpField('nativeDaggerHTTPHost', 'HTTP Host header', 'Use the dial address', 253);
        httpPath.row.append(el('div', { class: 'hint', text: 'Leave blank for /tunnel. The setup link shares the HTTP settings with Kharej.' }));
        httpHost.row.append(el('div', { class: 'hint', text: 'Sets the Host header when Kharej makes the HTTP request.' }));
        tunnelFields.append(httpPath.row, httpHost.row);
        const optionalFields = el('div', { class: 'fgrid' }, [mtu.row, delay.row, nativeAddress]);
        const optional = el('div', { class: 'grp3', hidden: true }, [
          el('div', { class: 'gl3', text: 'Fine Tune' }), optionalFields,
        ]);
        for (const preset of ['balance', 'turbo', 'aggressive']) {
          const card = el('button', { type: 'button', class: 'rp', 'data-pre': preset });
          card.addEventListener('click', () => {
            const id = currentSetupID();
            setupChoices.set(id, preset);
            value.input.value = '';
            paintSetupTuning();
          });
          cards.append(card);
        }
        perf.append(group);
        opt.append(optional);
        return { group, cards, summary, tunnelFields, performanceFields, optional, optionalFields,
          value, delay, mtu, httpPath, httpHost, mux: addMuxOptional(opt),
          waterWallFields, wwIranMTU, wwPeerMTU };
      }

      function currentSetupID() {
        return chosen.direction === 'direct' ? chosen.carrier : chosen.transport;
      }

      function paintSetupTuning() {
        if (!setupGroups) return;
        const profile = setupProfile(), id = currentSetupID();
        const { group, optional, cards, summary, tunnelFields, performanceFields, optionalFields,
          value, delay, mtu, httpPath, httpHost, mux, waterWallFields, wwIranMTU, wwPeerMTU } = setupGroups;
        const nativeMux = !!profile && nativeMuxOptional();
        const hasDaggerRoute = daggerRoute();
        const showAddress = !!reverseSetup()?.native && !nativeMux && !hasDaggerRoute;
        const sameKnob = !!profile && profile.label === profile.overrideLabel;
        group.hidden = !profile;
        const waterWall = waterWallSelected();
        performanceFields.hidden = !sameKnob;
        waterWallFields.hidden = !waterWall;
        value.row.hidden = waterWall;
        optional.hidden = !profile || nativeMux ||
          (sameKnob && !profile.restart && !profile.mtu && !(showAddress && !hasDaggerRoute));
        optional.style.display = '';
        mux.group.hidden = !nativeMux;
        httpPath.row.hidden = !hasDaggerRoute;
        httpHost.row.hidden = !daggerUpgrade();
        nativeAddress.hidden = !showAddress;
        const addressFields = hasDaggerRoute ? tunnelFields : optionalFields;
        if (nativeAddress.parentElement !== addressFields) addressFields.append(nativeAddress);
        if (!profile) {
          lastSetupProfile = null;
          return;
        }
        if (pairedDirect() || officialDagger() === 'tun' || directSetup()?.mode === 'matrix' || directSetup()?.pengu) {
          const standard = group.parentElement.querySelector('[data-mode="dir"]');
          if (standard) standard.hidden = true;
        }
        if (id !== lastSetupProfile) {
          value.input.value = delay.input.value = mtu.input.value = '';
          lastSetupProfile = id;
        }
        const preset = setupChoices.get(id) || 'turbo';
        const applies = profile.values[preset];
        if (waterWall) wwIranMTU.input.placeholder = wwPeerMTU.input.placeholder = String(applies);
        for (const card of cards.children) {
          const selected = card.dataset.pre === preset;
          card.classList.toggle('on', selected);
          card.setAttribute('aria-pressed', String(selected));
        }
        summary.innerHTML = `<div class="rp-sets-h"><b>What ${preset[0].toUpperCase() + preset.slice(1)} sets</b></div>
          <div class="rp-sets-g"><div><span>${esc(profile.label)}</span><b>${esc(String(applies))}</b></div></div>`;
        const valueFields = sameKnob ? performanceFields : optionalFields;
        if (value.row.parentElement !== valueFields) valueFields.prepend(value.row);
        value.title.textContent = profile.overrideLabel;
        value.input.min = profile.min;
        value.input.max = profile.max;
        value.input.placeholder = sameKnob ? applies : 'Automatic';
        delay.row.hidden = !profile.restart;
        delay.input.placeholder = id?.startsWith('gost-direct') || id === 'omni-hysteria' ? '3' : '2';
        mtu.row.hidden = !profile.mtu;
      }

      function setupTuningPayload() {
        const profile = setupProfile();
        if (!profile) return undefined;
        return {
          preset: setupChoices.get(currentSetupID()) || 'turbo',
          value: readTuningField(setupGroups.value),
          restartSeconds: readTuningField(setupGroups.delay),
          mtu: readTuningField(setupGroups.mtu),
        };
      }

      function readTuningField({ input, row, title }) {
        if (row.hidden || !input.value) return undefined;
        if (!input.checkValidity()) throw new Error(`${title.textContent} must be between ${input.min} and ${input.max}.`);
        return input.valueAsNumber;
      }

      function backhaulMuxPayload() {
        if (!nativeMuxOptional()) return undefined;
        const m = setupGroups.mux;
        return {
          kharejDial: m.dial.input.value.trim(),
          kharejBackups: m.backups.input.value.trim(),
          heartbeatSeconds: readTuningField(m.heartbeat),
          timeoutSeconds: readTuningField(m.timeout),
          ipv6: m.ipv6.checked(),
          proxyProtocol: m.proxy.checked(),
          limits: { maxConnections: readTuningField(m.conns), bandwidthMbps: readTuningField(m.bandwidth) },
        };
      }

      function addDaggerFields(details) {
        const reverse = details.querySelector('#atok').closest('.grp3');
        const group = el('div', { class: 'grp3', hidden: true });
        const title = el('div', { class: 'gl3', text: 'Dagger settings' });
        const grid = el('div', { class: 'fgrid' });
        const rows = new Map();
        const add = (name, label, modes, attrs = {}, hint = '') => {
          const id = 'dagger-' + name;
          const row = el('div', { class: 'f3' }, el('label', { for: id, text: label }));
          const { choices, ...inputAttrs } = attrs;
          const input = choices
            ? el('select', { id }, choices.map(([v, text]) => el('option', { value: v, text })))
            : el('input', { id, type: 'text', autocomplete: 'off', ...inputAttrs });
          row.append(input);
          if (hint) row.append(el('div', { class: 'hint', text: hint }));
          grid.append(row);
          rows.set(name, { row, input, modes });
          return input;
        };
        add('name', 'Tunnel name', ['tun']);
        const license = add('license', 'DaggerConnect license PSK', null, {
          type: 'password', maxlength: 4096, required: true, spellcheck: 'false', autocomplete: 'new-password',
        }, 'Enter the license issued by DaggerConnect. The private Setup command pairs the two servers.');
        const show = el('button', { type: 'button', class: 'mini3', text: 'Show', 'aria-controls': license.id });
        const secret = el('div', { class: 'withb' });
        license.before(secret);
        secret.append(license, show);
        show.addEventListener('click', () => {
          const reveal = license.type === 'password';
          license.type = reveal ? 'text' : 'password';
          show.textContent = reveal ? 'Hide' : 'Show';
        });
        add('iranTunnelIP', 'Iran TUN address', ['tun'], { placeholder: '10.201.8.1' });
        add('kharejTunnelIP', 'Kharej TUN address', ['tun'], { placeholder: '10.201.8.2' });
        const profile = add('tunProfile', 'TUN encapsulation', ['tun'], {
          choices: ['tcp', 'udp', 'icmp', 'gre', 'ipip', 'bip'].map(name => [name, name.toUpperCase()]),
        });
        profile.addEventListener('change', paintDaggerFields);
        add('tunPort', 'TUN service port', ['tun'], { type: 'number', min: 1, max: 65535, value: 443 });
        add('ports', 'Forwarded ports (optional)', ['tun'], { placeholder: 'tcp:443=8443, udp:53' });
        const quantumModes = ['quantum', 'quantum-gaming'];
        add('quantumMTU', 'Quantum MTU', quantumModes, { type: 'number', min: 512, max: 9000, placeholder: '1350' });
        add('quantumCipher', 'Quantum header cipher', quantumModes, { choices: [['aes', 'AES'], ['salsa20', 'Salsa20'], ['none', 'None']] });
        add('httpPath', 'HTTP path', ['xhttps'], { placeholder: '/api/v2', maxlength: 512 });
        add('uploadMode', 'Upload mode', ['xhttps'], {
          choices: [['auto', 'Auto'], ['stream-up', 'Streaming'], ['packet-up', 'Sequenced']],
        });
        add('certFile', 'TLS certificate path on Iran', ['xhttps'], { placeholder: '/etc/ssl/certs/tunnel.pem' },
          'The official core reads this PEM certificate from the Iran server.');
        add('keyFile', 'TLS private key path on Iran', ['xhttps'], { placeholder: '/etc/ssl/private/tunnel.key' });
        group.append(title, grid);
        reverse.after(group);
        return { group, reverse, rows };
      }

      function paintDaggerFields() {
        if (!daggerFields) return;
        const mode = officialDagger();
        const { group, reverse, rows } = daggerFields;
        group.hidden = !mode;
        if (!mode) return;
        const anchor = mode === 'tun' ? directFields : reverse;
        if (group.previousElementSibling !== anchor) anchor.after(group);
        group.style.display = '';
        for (const { row, modes } of rows.values()) row.hidden = !!modes && !modes.includes(mode);
        const profile = rows.get('tunProfile').input.value;
        rows.get('tunPort').row.hidden = mode !== 'tun' || !['tcp', 'udp'].includes(profile);
      }

      function addPenguFields(details, opt) {
        const settings = el('div', { class: 'grp3', hidden: true }, [
          el('div', { class: 'gl3', text: 'PenguTunnel settings' }),
        ]);
        const optional = el('div', { class: 'grp3', hidden: true }, [
          el('div', { class: 'gl3', text: 'PenguTunnel options' }),
        ]);
        const grid = el('div', { class: 'fgrid' });
        const fine = el('div', { class: 'fgrid' });
        settings.append(grid);
        optional.append(fine);
        details.append(settings);
        opt.append(optional);
        const rows = new Map();
        const add = (parent, key, title, attrs, when, hint) => {
          const { choices, ...inputAttrs } = attrs;
          const id = 'pengu-' + key;
          const input = choices
            ? el('select', { id }, choices.map(([value, text]) => el('option', { value, text })))
            : el('input', { id, type: 'text', ...inputAttrs });
          const row = el('div', { class: 'f3 full' }, el('label', { for: id, text: title }), input);
          if (hint) row.append(el('div', { class: 'hint', text: hint }));
          rows.set(key, { row, input, when });
          parent.append(row);
          return input;
        };
        const num = (min, max, placeholder = 'Automatic') => ({ type: 'number', min, max, placeholder });
        const only = id => mode => mode.id === id;
        const peer = add(grid, 'kharejHost', 'Kharej server address', { placeholder: 'IP or hostname', required: true },
          () => chosen.direction === 'direct', 'Used by Direct and IPX to reach the other endpoint.');
        const key = add(grid, 'secret', 'Security token', { placeholder: 'Automatically generated', autocomplete: 'off', maxlength: 44 });
        const random = el('button', { type: 'button', class: 'mini3', text: 'Random' });
        const withButton = el('div', { class: 'withb' });
        key.before(withButton);
        withButton.append(key, random);
        random.addEventListener('click', () => {
          key.value = btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(32))));
        });
        add(grid, 'iranTunnel', 'Iran tunnel IPv4/CIDR', { placeholder: 'Automatically assigned' }, mode => mode.tun);
        add(grid, 'kharejTunnel', 'Kharej tunnel IPv4/CIDR', { placeholder: 'Automatically assigned' }, mode => mode.tun,
          'Leave both blank to have BackPack allocate a private subnet.');
        add(grid, 'httpPath', 'HTTP POST path', { placeholder: '/xhttp', maxlength: 256 },
          mode => mode.id === 'xhttp' || mode.id === 'xhttps');
        add(grid, 'tlsName', 'TLS server name', { placeholder: 'Use the core default' },
          mode => mode.id === 'tls' || mode.id === 'xhttps');
        add(fine, 'iranHost', 'Iran IP or hostname', { placeholder: 'Automatically detected' });
        add(fine, 'healthPort', 'Health check port', num(1, 65535));
        add(fine, 'workers', 'Worker CPU limit', num(0, 64, '0 = automatic'));
        add(fine, 'window', 'Stream window (MB)', num(0, 256, '0 = automatic'), mode => !mode.tun);
        add(fine, 'cipher', 'Encryption', { choices: [['aes-256-gcm', 'AES-256-GCM'], ['chacha20-poly1305', 'ChaCha20-Poly1305']] });
        add(fine, 'header', 'TCP carrier header',
          { choices: [['', 'Plain TCP'], ...[1, 2, 3].map(n => ['PGprotocol-NUM' + n, 'PGprotocol-NUM' + n])] },
          only('tcpfwd'));
        add(fine, 'icmpType', 'ICMP type', num(0, 255, 'Core default'), only('icmp'));
        add(fine, 'icmpCode', 'ICMP code', num(0, 255, '0'), only('icmp'));
        return { settings, optional, rows, peer };
      }

      function paintPenguFields() {
        if (!penguFields) return;
        const mode = penguSetup();
        const { settings, optional, rows, peer } = penguFields;
        settings.hidden = optional.hidden = !mode;
        if (!mode) return;
        settings.style.display = optional.style.display = '';
        peer.placeholder = mode.ipx ? 'Kharej public IPv4' : 'IP or hostname';
        for (const { row, when } of rows.values()) row.hidden = !!when && !when(mode);
      }

      function paintAddressFamily() {
        if (!setupGroups) return;
        const direct = chosen.direction === 'direct';
        if (direct) {
          if (addressRow.nextElementSibling !== directFields) directFields.before(addressRow);
        } else if (addressRow.parentElement !== setupGroups.tunnelFields) {
          setupGroups.tunnelFields.prepend(addressRow);
        }
        const allowed = setupFamilies();
        for (const option of addressChoice.options) option.disabled = !allowed.includes(option.value);
        addressChoice.value = allowed.includes(preferredFamily) ? preferredFamily : allowed[0];
        addressChoice.disabled = allowed.length === 1;
        familyHint();
      }

      function stageSingle() {
        const body = root.querySelector('.body5');
        const side = root.querySelector('.step[data-s="0"]');
        const details = root.querySelector('.step[data-s="1"]');
        if (!body || !side || !details || root.dataset.staged) return;
        root.dataset.staged = '1';
        root.querySelector('#nodeGrp')?.remove();

        root.querySelectorAll('.step3rev .grp3, .step3rev .dr2b')
          .forEach(g => { g.dataset.mode = 'rev'; });
        root.querySelectorAll('.step3direct .grp3, .step3direct .dr2b')
          .forEach(g => { g.dataset.mode = 'dir'; });

        /* This server is the Iran end.
         *
         * The panel runs where tunnels are started from, and that is Iran: the
         * kharej end is made from the setup link at the end, with one line, and
         * a kharej-side form here was a second way to do the same thing that
         * nobody needed. The side question is gone; the side buttons stay in
         * the markup, hidden, because the rest of the form listens to them. */
        chosen.side = 'server';
        root.querySelector('[data-fn="setSide"][data-args*="server"]')?.click();
        side.querySelector('.choices')?.setAttribute('hidden', '');
        const lede = side.querySelector('.lede2');
        if (lede) lede.hidden = true;

        /* Reverse or direct, in cards under that. It was a small switch above
           the transport list, easy to miss and the one choice that changes
           every field after it. The switch is kept, hidden, because it is what
           the rest of the form listens to. */
        const kind = el('div', { class: 'typegrp' });
        kind.innerHTML = `<div class="here-card">
            <span class="here-flag"><svg viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="7" rx="2"/><path d="M7 6.5h.01M7 17.5h.01"/></svg></span>
            <div><b>Iran — this server</b><small>This end is built here. The kharej is set up from the one line
              you get at the end: paste it there and the tunnel comes up.</small></div></div>
          <div class="lede2 kindq">How do the two servers reach each other?</div>
          <div class="grp3 kindgrp"><div class="choices">
            <button type="button" class="ch4" data-kind="rev">
              <span class="ic4"><svg viewBox="0 0 24 24"><path d="M20 12H4"/><path d="M10 6l-6 6 6 6"/></svg></span>
              <b>Reverse<span class="rec">usual</span></b>
              <i>The kharej dials in to this server. TCP, UDP, WebSocket or HTTPS — try this first.</i>
              <div class="diagram"><span class="route"><span>kharej</span><i class="rt"></i><span>Iran</span></span></div></button>
            <button type="button" class="ch4" data-kind="dir">
              <span class="ic4"><svg viewBox="0 0 24 24"><path d="M4 12h16"/><path d="M14 6l6 6-6 6"/></svg></span>
              <b>Direct</b>
              <i>Iran connects to Kharej, or both ends exchange packets directly.</i>
              <div class="diagram"><span class="route"><span>Iran</span><i class="rt"></i><span>kharej</span></span></div></button>
          </div></div>`;
        side.append(kind);
        const swap = root.querySelector('.modeswap');
        const markKind = () => kind.querySelectorAll('[data-kind]').forEach(b =>
          b.classList.toggle('on', b.dataset.kind === (chosen.direction === 'direct' ? 'dir' : 'rev')));
        kind.addEventListener('click', ev => {
          const b = ev.target.closest('[data-kind]');
          if (!b) return;
          swap?.querySelector(`[data-args*="${b.dataset.kind}"]`)?.click();
          markKind();
        });
        if (swap) swap.hidden = true;
        markKind();
        const pane = () => el('div', { class: 'step', hidden: true });
        const perf = pane(), opt = pane();
        root.querySelectorAll('.grp3').forEach(g => {
          const head = g.querySelector('.gl3')?.childNodes[0]?.textContent?.trim();
          if (head === 'Performance') perf.append(g);
          if (head === 'Optional') opt.append(g);
        });
        const directPerformance = perf.querySelector('[data-mode="dir"]');
        const directLimits = directPerformance?.querySelector(':scope > .fgrid');
        if (directLimits) opt.append(el('div', { class: 'grp3', 'data-mode': 'dir' }, [
          el('div', { class: 'gl3', text: 'Forwarding limits' }), directLimits,
        ]));
        setupGroups = addSetupTuning(details.querySelector('#atok').closest('.fgrid'), perf, opt);
        daggerFields = addDaggerFields(details);
        penguFields = addPenguFields(details, opt);
        opt.append(mtOptional);
        /* PCK's fine-tuning goes with the presets it overrides, on the same
           step, rather than among the fields that name the tunnel. */
        const pckDrawer = root.querySelector('#dw-pck');
        if (pckDrawer) perf.append(pckDrawer);
        details.after(perf);
        perf.after(opt);
        dressPresets();

        [...body.querySelectorAll('.step')].forEach((x, i) => {
          x.dataset.s = String(i);
          x.hidden = i !== 0;
        });
        const rail = root.querySelector('.steps');
        if (rail) {
          rail.innerHTML = '';
          SINGLE.forEach((lb, i) => {
            if (i) rail.append(el('span', { class: 'bar4' }));
            rail.append(el('span', { class: 'st2' + (i ? '' : ' on'), dataset: { s: String(i) } }, [
              el('span', { class: 'n3', text: String(i + 1) }),
              el('span', { class: 'lb4', text: lb }),
            ]));
          });
        }
        const back = root.querySelector('#backb');
        if (back) back.disabled = true;
        paintNav(0);
        applyShape();
      }
      function stageReplacement() {
        const steps = paneList();
        const connection = steps[1];
        root.querySelector('.dh .ttl > div').textContent = 'Edit ' + replacement.name;
        root.querySelector('#dlgSub').textContent = 'Choose a transport and configure both ends.';
        const settings = el('button', { type: 'button', class: 'mini3', text: 'Edit settings' });
        settings.addEventListener('click', () => go('/t/' + encodeURIComponent(replacement.name) + '/edit?settings=1'));
        root.querySelector('.dh button.x').before(settings);
        root.querySelector('#trlist').hidden = true;
        directChoices.classList.add('rebuild-hidden');
        const selectField = (id, label) => {
          const select = el('select', { id });
          const field = el('div', { class: 'f3' }, [el('label', { for: id, text: label }), select]);
          return { field, select };
        };
        const direction = selectField('rebuildDirection', 'Tunnel type');
        const family = selectField('rebuildFamily', 'Transport family');
        const transport = selectField('rebuildTransport', 'Transport');
        const option = (value, text) => el('option', { value, text });
        direction.select.append(option('reverse', 'Reverse'), option('direct', 'Direct'));
        connection.prepend(el('div', { class: 'grp3' }, [
          el('div', { class: 'fgrid rebuild-selects' }, [direction.field, family.field, transport.field]),
          el('p', { class: 'hint', text: 'Applying changes switches the transport. Run the new Setup command on Kharej.' }),
        ]));

        const display = entry => transportDisplay(entry.value, entry.label);
        const related = entries => entries.map(entry => {
          const { source } = display(entry);
          return { ...entry, group: source?.split(' \u00b7 ')[0] || (entry.value.startsWith('gost-') ? 'GOST' : 'BackPack') };
        });
        const catalog = {
          reverse: (opts.families || []).flatMap(group => group.entries.map(entry => ({ ...entry, group: group.label })))
            .concat(related([...managedReverse.values()])),
          direct: related([...(directOpts?.carriers || []), ...managedDirect.values()]),
        };
        const switchDirection = value => {
          direction.select.value = value;
          const key = value === 'direct' ? 'dir' : 'rev';
          root.querySelector('.modeswap [data-args*="' + key + '"]')?.click();
          root.querySelectorAll('[data-kind]').forEach(b => b.classList.toggle('on', b.dataset.kind === key));
        };
        const selectTransport = value => {
          if (!value) return;
          transport.select.value = value;
          const fn = chosen.direction === 'direct' ? 'setCar' : 'setTr';
          const button = [...root.querySelectorAll('[data-fn="' + fn + '"]')]
            .find(b => b.dataset.args === "'" + value + "'");
          button?.click();
        };
        const paintTransportSelect = preferred => {
          const entries = catalog[chosen.direction];
          const groups = [...new Set(entries.map(item => item.group))];
          const selectedFamily = family.select.value;
          family.select.replaceChildren(option('', 'All families'), ...groups.map(group => option(group, group)));
          if (groups.includes(selectedFamily)) family.select.value = selectedFamily;
          transport.select.replaceChildren();
          for (const group of groups.filter(g => !family.select.value || g === family.select.value)) {
            const list = entries.filter(entry => entry.group === group).map(entry => {
              const { name, source } = display(entry);
              return option(entry.value, source && source !== group ? name + ' \u00b7 ' + source : name);
            });
            transport.select.append(el('optgroup', { label: group }, list));
          }
          const hasPreferred = [...transport.select.options].some(e => e.value === preferred);
          selectTransport(hasPreferred ? preferred : transport.select.options[0]?.value);
        };
        direction.select.addEventListener('change', () => {
          switchDirection(direction.select.value);
          family.select.value = '';
          paintTransportSelect(chosen.direction === 'direct' ? chosen.carrier : chosen.transport);
        });
        family.select.addEventListener('change', () => paintTransportSelect(transport.select.value));
        transport.select.addEventListener('change', () => selectTransport(transport.select.value));
        root.querySelectorAll('[data-kind]').forEach(b => b.addEventListener('click', () => {
          direction.select.value = chosen.direction;
          family.select.value = '';
          paintTransportSelect(chosen.direction === 'direct' ? chosen.carrier : chosen.transport);
        }));
        switchDirection(replacement.direction);
        paintTransportSelect(replacement.transport);

        for (const field of [root.querySelector('#aname'), directNameRow.querySelector('input')]) {
          field.value = replacement.name;
          field.readOnly = true;
        }
        const ports = (replacement.ports || []).filter(Boolean).join(', ');
        reverseForward.value = directForward.value = ports;
        if (replacement.port) {
          (chosen.direction === 'direct' ? directPort : reversePort).value = replacement.port;
        }
        const preset = replacement.preset;
        if (preset && !setupProfile()) {
          [...root.querySelectorAll('.rp')].find(b => inShape(b) &&
            (b.dataset.pre || b.querySelector('.key2')?.textContent) === preset)?.click();
        }
        steps.forEach((pane, i) => { pane.hidden = i !== 1; });
        root.dispatchEvent(new CustomEvent('step', { detail: { at: 1, of: steps.length } }));
      }

      stageSingle();
      if (replacement) {
        await carriersReady;
        if (!root.isConnected) return;
        stageReplacement();
      }
      reveal();
      randomGreKey.addEventListener('click', async () => {
        const carrier = chosen.carrier;
        randomGreKey.disabled = true;
        try {
          const { greKey } = await api.directDefaults('iran');
          if (chosen.carrier === carrier && !randomGreKey.hidden) {
            root.querySelector('#directGreKey').value = String(greKey);
          }
        } catch (e) { oops(e); }
        finally { randomGreKey.disabled = false; }
      });

      /* The token, made here.
       *
       * The form still has the two-pass wording: one side "creates" the secret
       * with a Copy button and the other "pastes" it. With the setup link that
       * split is gone — whichever end is built first makes the token and the
       * link carries it to the other one. So every token field starts filled
       * with a fresh one from the server and stays editable, for the case where
       * the other end already exists and its token is the one to use. The
       * creating field on the reverse side was drawn with no name at all, which
       * is why a create from here used to be refused for having no token. */
      const tokenFields = [...root.querySelectorAll('#atok, [name="token"]')];
      tokenFields.forEach(i => {
        i.name = 'token';
        i.removeAttribute('data-unwired');
        const hint = i.closest('.f3')?.querySelector('.hint');
        if (hint) hint.textContent = 'Made for this tunnel — the setup link carries it to the other server. '
          + 'If that server is already set up, paste its token here instead.';
      });
      const reverseToken = root.querySelector('#atok');
      const randomToken = el('button', { type: 'button', class: 'mini3', text: 'Random' });
      reverseToken.closest('.withb').append(randomToken);
      randomToken.addEventListener('click', async () => {
        randomToken.disabled = true;
        try {
          reverseToken.value = (await api.tunnelToken()).token || '';
        } catch (e) { oops(e); }
        finally { randomToken.disabled = false; }
      });
      api.tunnelToken()
        .then(r => tokenFields.forEach(i => { if (!i.value || i.defaultValue === i.value) i.value = r.token || ''; }))
        .catch(() => tokenFields.forEach(i => { i.value = ''; i.placeholder = 'type a long random token'; }));

      function mtProxyMapping() {
        if (!mtMode.value) return '';
        const entry = mtEntry.value.trim();
        const valid = port => /^\d+$/.test(port) && Number(port) >= 1 && Number(port) <= 65535;
        if (!valid(entry)) return '';
        return (mtMode.value === 'web' ? '127.0.0.1:' : '') + entry + '=' + mtExit.value.trim();
      }

      function scheduleForwardCheck() {
        clearTimeout(forwardCheckTimer);
        forwardCheckRequest?.abort();
        for (const [input, note] of forwardErrors) {
          input.removeAttribute('aria-invalid');
          note.hidden = true;
          note.textContent = '';
        }
        forwardCheckTimer = setTimeout(checkForwardFields, 300);
      }

      async function checkForwardFields() {
        clearTimeout(forwardCheckTimer);
        forwardCheckRequest?.abort();
        if (!root.isConnected || replacement || chosen.side !== 'server') return;
        const direct = chosen.direction === 'direct';
        const input = direct ? directForward : reverseForward;
        const relevant = node => !node.closest('[hidden]:not(.step)');
        const checked = name => {
          const node = root.querySelector('input[name="' + name + '"]');
          return !!node?.checked && !node.dataset.drawn && relevant(node.parentElement);
        };
        const udpOnly = !direct && ['udp', 'udp-padder'].includes(chosen.transport);
        const request = {
          ports: relevant(input) ? input.value.trim() : '',
          proxy: mtProxyMapping(),
          tcp: !udpOnly,
          udp: udpOnly || checked('tune.acceptUDP') || checked('routedAcceptUdp'),
          tunnelPort: !direct && relevant(reversePort) ? reversePort.value.trim() : '',
          tunnelTransport: !direct ? chosen.transport || '' : '',
        };
        if (!request.ports && !request.proxy) return;
        const controller = new AbortController();
        forwardCheckRequest = controller;
        let errors;
        try {
          errors = await api.forwardPortCheck(request, controller.signal);
        } catch {
          return; // The create handler still validates the form.
        }
        if (controller.signal.aborted) return;
        for (const [field, message] of [[input, errors.ports], [mtEntry, errors.proxy]]) {
          const note = forwardErrors.get(field);
          note.textContent = message;
          note.hidden = !message;
          if (message) field.setAttribute('aria-invalid', 'true');
          else field.removeAttribute('aria-invalid');
        }
        return errors.ports ? input : errors.proxy ? mtEntry : null;
      }

      for (const field of [reverseForward, directForward, reversePort, mtEntry]) {
        field.addEventListener('input', scheduleForwardCheck);
      }
      mtMode.addEventListener('change', scheduleForwardCheck);

      /* Building the tunnel.
       *
       * It used to hang off a button that is not in this markup, so the form
       * collected everything and posted it nowhere. It is a function now, run
       * when the wizard reaches its last step — which is also where the result
       * is shown, so pressing Continue on the step before it is the commit.
       */
      async function createDestination(kind, form, create, response = r => r) {
        if (!replacement) return { r: response(await create(form)), name: form.name };
        const result = await api.tunnelRebuild({ name: replacement.name, revision: replacement.revision, kind, form });
        return { r: result.setup.pending ? result.setup : result, name: form.name };
      }

      async function submitTunnel() {
        /* Only what applies: a hidden field belongs to the other side or the
           other kind of tunnel, and sending it would describe a tunnel nobody
           asked for.
           
           A step that is not the one on screen is hidden too, and that is a
           different thing entirely. The tunnel is built from the last step, by
           which point every step holding a field is hidden — so testing for any
           hidden ancestor collected nothing at all and posted an empty form.
           The panes are skipped over; everything else still counts. */
        const irrelevant = n => {
          for (let at = n.parentElement; at && at !== root; at = at.parentElement) {
            if (at.hidden && !at.classList.contains('step')) return true;
          }
          return false;
        };
        const payload = {};
        root.querySelectorAll('input[name], select[name]').forEach(n => {
          if (irrelevant(n)) return;
          /* A drawer switch or menu nobody touched says nothing. Posting what
             it was drawn with sent every knob in Fine Tune on every create, and
             a tunnel given its own numbers is a tunnel off its preset: pick
             Aggressive, get no preset line and the drawing's buffers — which
             are Turbo's. Touched, it is sent, off included. */
          if (n.dataset.drawn) return;
          const wired = n.hidden && n.type === 'checkbox';
          const v = n.type === 'checkbox' ? n.checked : n.value.trim();
          if (v === '' || (v === false && !wired)) return;
          const keys = n.name.split('.'), last = keys.pop();
          let at = payload;
          for (const k of keys) at = at[k] ??= {};
          at[last] = NUMERIC.has(n.name) ? Number(v) : v;
        });
        payload.name ||= '';
        if (mtMode.value) {
          const range = /^\d+$/;
          const entry = mtEntry.value.trim(), exit = mtExit.value.trim();
          if (![entry, exit].every(port => range.test(port) && Number(port) >= 1 && Number(port) <= 65535)) {
            throw new Error('In Optional → Telegram MTProxy, enter both TCP ports between 1 and 65535.');
          }
          payload.ports = [payload.ports, mtProxyMapping()].filter(Boolean).join(', ');
        }
        if (chosen.direction === 'direct') {
          payload.side = chosen.side === 'server' ? 'iran' : 'kharej';
          payload.carrier = chosen.carrier;
        } else {
          payload.role = chosen.side;
          payload.transport = chosen.transport;
        }
        if (chosen.preset) payload.preset = chosen.preset;
        const key = payload.greKey;
        if (key !== undefined && (!Number.isInteger(key) || key < 0 || key > 4294967295)) {
          throw new Error('Enter a GRE key from 0 to 4294967295.');
        }

        const setup = reverseSetup();
        const tuning = setupTuningPayload();
        const dagger = officialDagger();
        const setupFamily = addressChoice.value;
        const pengu = penguSetup();
        if (pengu) {
          const form = { name: payload.name, transport: pengu.mode, direction: chosen.direction,
            family: setupFamily, ports: payload.ports || '', preset: tuning?.preset };
          if (!pengu.ipx) {
            const port = String(payload.tunnelPort || '');
            if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) {
              throw new Error('Enter a PenguTunnel port from 1 to 65535.');
            }
            form.port = Number(port);
            form.pool = tuning?.value;
          }
          if (pengu.tun) form.mtu = pengu.ipx ? tuning?.value : tuning?.mtu;
          for (const [name, { row, input }] of penguFields.rows) {
            if (row.hidden) continue;
            if (!input.checkValidity()) throw new Error('Check ' + row.querySelector('label').textContent + '.');
            if (input.value.trim() !== '') form[name] = input.type === 'number' ? input.valueAsNumber : input.value.trim();
          }
          return createDestination('pengu', form, api.penguCreate, r => r.setup);
        }
        if (easyTierSelected()) {
          const port = String(payload.tunnelPort || '2090').trim();
          if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) {
            throw new Error('Enter an EasyTier port from 1 to 65535.');
          }
          return createDestination('direct-peer', {
            ...payload, side: 'iran', carrier: 'easytier', tunnelPort: port, setupFamily,
            easytierProtocol: easyTierProtocol.value, easytierReverse: chosen.direction === 'reverse',
            acceptUdp: !!payload.routedAcceptUdp, preset: tuning?.preset, mtu: tuning?.value,
          }, api.directPeerSetup);
        }
        if (matrixSelected()) {
          const port = String(payload.tunnelPort || '').trim();
          if (port && (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535)) {
            throw new Error('Enter a Tinc port from 1 to 65535.');
          }
          return createDestination('matrix', {
            name: payload.name, direction: chosen.direction, subnet: matrixSubnet.value.trim(), port: Number(port),
            ports: payload.ports || '', acceptUdp: !!payload.routedAcceptUdp, tuning, setupFamily,
          }, api.matrixCreate);
        }
        if (dagger) {
          const form = { name: payload.name, port: Number(payload.tunnelPort || 0), ports: payload.ports || '',
            transport: dagger, tuning, setupFamily };
          for (const [name, { row, input }] of daggerFields.rows) {
            if (row.hidden) continue;
            if (!input.checkValidity()) throw new Error('Check ' + row.querySelector('label').textContent + '.');
            if (input.type === 'number') {
              if (input.value) form[name] = input.valueAsNumber;
            } else {
              form[name] = input.value.trim();
            }
          }
          if (dagger !== 'tun' && (!/^\d+$/.test(payload.tunnelPort || '') || form.port < 1 || form.port > 65535)) {
            throw new Error('Enter a tunnel port from 1 to 65535.');
          }
          return createDestination('dagger', form, api.daggerCreate, r => r.setup?.pending ? r.setup : r);
        }
        if (backhaulTUN() === 'tun-ipx') {
          if (!backhaulHealthPort.checkValidity()) throw new Error('Enter a health port from 1 to 65535.');
          return createDestination('backhaul', {
            name: payload.name, transport: 'tun-ipx', setupFamily, tuning, ports: payload.ports || '',
            ipxProfile: backhaulProfile.value, healthPort: Number(backhaulHealthPort.value || 0),
            iranTunnel: backhaulIranTUN.value.trim(), kharejTunnel: backhaulKharejTUN.value.trim(),
          }, api.backhaulCreate, r => r.setup);
        }
        if (setup?.native) {
          if (!/^\d+$/.test(payload.tunnelPort || '') || Number(payload.tunnelPort) < 1 || Number(payload.tunnelPort) > 65535) {
            throw new Error('Enter a tunnel port from 1 to 65535.');
          }
          return createDestination('backhaul', {
            name: payload.name, transport: setup.mode, port: Number(payload.tunnelPort),
            ports: payload.ports || '', secret: payload.token || '', iranHost: payload.iranHost || '',
            tuning, setupFamily, mux: backhaulMuxPayload(),
            iranTunnel: backhaulTUN() ? backhaulIranTUN.value.trim() : undefined,
            kharejTunnel: backhaulTUN() ? backhaulKharejTUN.value.trim() : undefined,
            httpPath: daggerRoute() ? setupGroups.httpPath.input.value.trim() : undefined,
            httpHost: daggerUpgrade() ? setupGroups.httpHost.input.value.trim() : undefined,
          }, api.backhaulCreate);
        }
        const routed = setup || directSetup();
        if (routed) {
          return createDestination('routed', {
            name: payload.name, transport: routed.mode, ports: payload.ports || '', secret: payload.token || '',
            peerAddr: payload.peerAddr || '', port: payload.tunnelPort || '', acceptUdp: !!payload.routedAcceptUdp, tuning,
          }, api.routedPeerSetup);
        }
        if (waterWallSelected()) {
          const protocol = waterWallProtocol.valueAsNumber;
          if (!waterWallProtocol.checkValidity() || !Number.isInteger(protocol)) {
            throw new Error('Enter an IP protocol number from 1 to 254.');
          }
          if (protocol === 6) {
            throw new Error("Choose an IP protocol other than 6 to keep the peer's native TCP traffic separate.");
          }
          const localIp = waterWallIranIP.value.trim(), peerIp = waterWallPeerIP.value.trim();
          if (Boolean(localIp) !== Boolean(peerIp)) throw new Error('Enter both tunnel IPv4 addresses or leave both empty.');
          const { wwIranMTU, wwPeerMTU } = setupGroups;
          const mtu = setupProfile().values[tuning.preset];
          return createDestination('direct-peer', {
            ...payload, setupFamily, preset: tuning.preset, localIp, peerIp,
            waterwallProtocol: protocol,
            mtu: readTuningField(wwIranMTU) ?? mtu,
            waterwallPeerMtu: readTuningField(wwPeerMTU) ?? mtu,
            waterwallWorkers: readTuningField(wwIranWorkers) ?? 0,
            waterwallPeerWorkers: readTuningField(wwPeerWorkers) ?? 0,
          }, api.directPeerSetup);
        }
        if (automaticDirect()) {
          if (pairedDirect()) {
            for (const key of ['greLocal', 'localIp', 'peerIp']) delete payload[key];
          }
          if (tuning) {
            payload.preset = tuning.preset;
            payload.mtu = tuning.value;
          }
          payload.setupFamily = setupFamily;
          return createDestination('direct-peer', payload, api.directPeerSetup);
        }
        if (chosen.direction === 'reverse') payload.setupFamily = setupFamily;
        // Only this end: the other is built from its setup link.
        return chosen.direction === 'direct'
          ? createDestination('direct', payload, api.directCreate)
          : createDestination('reverse', payload, api.tunnelCreate);
      }

      ctx.setTeardown(close);
    },
  }).catch(oops);
}
