# Installing Backpack

## The normal way

On each server, clone the fork and install from its `main` branch:

```bash
git clone --depth 1 --branch main https://github.com/Alfred-1313/BackPack.git &&
  (cd BackPack && sudo env BP_BUILD_FROM_SOURCE=1 bash install.sh)
```

The installer builds the checked-out source with Go and opens the menu on an
interactive terminal after a successful installation. The final report shows
which components are ready, already present, failed, or will be installed when
first used. It records the latest run in `/var/log/backpack/install.log`, which
is readable by root.

On a supported online install, BackPack installs the official DaggerConnect
launcher. DaggerConnect obtains its licensed core when first used. Use
`BP_HELPERS` to select optional helpers. Matrix (Tinc) and EasyTier install
when first used if their cores are missing. If a requested helper fails, the
installer checks the remaining helpers and exits with a failure status.
Install the libpcap development headers and a C compiler first if you need PCK.
If you are already root, use `BP_BUILD_FROM_SOURCE=1 bash install.sh` inside the checkout.

Reopen the menu any time with:

```bash
sudo backpack
```

Everything lands in a tidy layout — the release bundle in `/root/BackPack`,
backups in `/root/BackPack/backups`, tunnel configs in `/etc/backpack`. See
[server layout](server-layout.md).

For a later source update, run `git pull --ff-only` in the fork's checkout
and rerun the installer with `BP_BUILD_FROM_SOURCE=1`.

---

## GOST v3 helper

On both Linux amd64 or arm64 servers, run the installer from the fork checkout
with the optional helper enabled.

```bash
sudo env BP_BUILD_FROM_SOURCE=1 BP_HELPERS=gost bash install.sh
```

The installer checks the official GOST v3.2.6 archive against a pinned SHA-256
digest and installs the executable in a versioned directory. See the
[GOST setup steps](transports.md#gost-v3-relay-over-tls).

---

## Offline install (the server cannot reach GitHub)

Download the release on any machine **with** internet, copy it to the server, and
install it there. Nothing is fetched from the VPS.

![Offline install](../img/offline-install.gif)

If the [fork's releases page](https://github.com/Alfred-1313/BackPack/releases) has
a binary release, download its archive for the server's architecture. Run `uname -m`:
`x86_64` → `backpack_linux_amd64.tar.gz`, `aarch64` → `backpack_linux_arm64.tar.gz`.

### With the installer (recommended)

It also records the layout for the uninstaller. Download `install.sh` and
`SHA256SUMS` alongside the archive, put all three in the **same folder** on the
VPS, and run it. It finds the local archive, verifies it against `SHA256SUMS`,
and never touches the network:

```bash
scp install.sh SHA256SUMS backpack_linux_amd64.tar.gz root@SERVER_IP:/root/
ssh root@SERVER_IP "cd /root && sudo bash install.sh"
```

### By hand

Upload the archive to the server, then as root:

```bash
sha256sum backpack_linux_amd64.tar.gz        # compare against SHA256SUMS
tar xzf backpack_linux_amd64.tar.gz
mkdir -p /etc/backpack /root/BackPack/backups
install -m 0755 backpack /usr/local/bin/backpack
echo /root/BackPack > /etc/backpack/install_path
sudo backpack
```

The `install_path` line is what the built-in uninstaller reads to know what to
remove; skip it and everything still runs, but uninstalling has to be done by
hand. `install -m 0755` already sets the executable bit, so no `chmod` is needed.

### Updating offline

The same way: repeat the steps with the newer archive. `install` replaces the
binary in place, and your tunnels in `/etc/backpack` are untouched. Restart them
afterwards with `sudo backpack` → **Manage → Restart ALL**.

---

## Updating online

The fork's source update uses the checkout above. **Main menu → 8) Update** follows
the original BackPack releases. It downloads a release, verifies it against the
published SHA-256, installs it, and **rolls back automatically** if a tunnel does
not come back up. Anything that cannot be verified is refused rather than
installed. [More](updates.md).

## Uninstalling

**Main menu → 9) Uninstall** removes everything Backpack installed.

---

<div dir="rtl">

## خلاصهٔ فارسی

**نصب عادی** شاخهٔ `main` فورک را کلون کن و دستور بالا را برای ساخت از سورس
اجرا کن. بعداً با `sudo backpack` منو را باز کن.

**نصب آفلاین (سروری که به گیت‌هاب دسترسی ندارد):** فایل ریلیز را روی یک ماشین با
اینترنت دانلود کن و به سرور کپی کن. با `uname -m` معماری را ببین: `x86_64` یعنی
amd64 و `aarch64` یعنی arm64. بهترین راه این است که `install.sh` و `SHA256SUMS`
را هم کنار آرشیو در **یک پوشه** بگذاری و اسکریپت را اجرا کنی — خودش فایل محلی را
پیدا و تأیید می‌کند و اصلاً به شبکه دست نمی‌زند. روش دستی هم در بالا آمده؛ فقط
یادت باشد خط `install_path` را بنویسی، چون حذف‌کنندهٔ داخلی از روی آن می‌فهمد چه
چیزی را پاک کند.

**آپدیت:** از منوی اصلی گزینهٔ ۸ — با تأیید SHA-256 و **بازگشت خودکار** اگر تونل
بالا نیامد. آپدیت آفلاین هم همان مراحل نصب با آرشیو جدید است و کانفیگ‌های
`/etc/backpack` دست‌نخورده می‌مانند.

</div>

---
[← Back to the docs index](README.md)

---

*Last verified against Backpack v2.0.0.*
