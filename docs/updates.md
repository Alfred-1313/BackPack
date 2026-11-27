# Updates & rollback

Choose **Update** (option 8) in the terminal menu to install the newest
published release from the fork. The menu and web panel are part of the same
binary. The update verifies the release, takes a restore point, then reloads
the running web panel. Running tunnels pick up the new binary on their next
restart.

## How it downloads

**Direct from GitHub, or through a tunnel peer** when this server cannot reach
GitHub itself. Both paths terminate TLS at GitHub, so the download is verified
against the release's published **SHA-256** and its publisher signature.

**An archive that cannot be verified is refused, not installed.** A server that
can reach neither path installs offline instead — see the offline install
section in the main README.

## Safety net

Before touching anything, Update saves a **restore point**. After installing it
checks the web panel and restores the previous binary if the panel fails to restart.
Use **Manage → Update Tools → Restore Points** for a manual rollback.

## Channels

Follow **stable** (default) or **beta** under **Manage → Update Tools → Release Channel**.

## Upgrading a very old install

From a clone-based install (≤ v1.2.0): run Update once; after that it is
release-based.

---

<div dir="rtl">

## خلاصهٔ فارسی

با انتخاب **Update** (گزینهٔ ۸) از منوی ترمینال، آخرین ریلیز فورک نصب می‌شود. پنل و منو همزمان به‌روز می‌شوند و تونل‌های فعال به کارشان ادامه می‌دهند.

**چطور دانلود می‌کند:** مستقیم از گیت‌هاب، یا **از طریق یک تونل** وقتی خود این
سرور به گیت‌هاب دسترسی ندارد. هر دو مسیر TLS را در گیت‌هاب terminate می‌کنند، پس
دانلود با **SHA-256** منتشرشدهٔ ریلیز تأیید می‌شود.
**آرشیوی که تأیید نشود نصب نمی‌شود، رد می‌شود.**

**تور ایمنی:** قبل از دست زدن به چیزی یک **restore point** ذخیره می‌کند، بعد از
نصب نتیجه را health-check می‌کند و **اگر چیزی بالا نیامد خودش برمی‌گردد عقب**.
دستی هم می‌شود از `Manage → Update Tools → Restore Points` برگشت.

**کانال:** پیش‌فرض **stable**؛ اگر بخواهی pre-release‌ها را هم بگیری، از
`Manage → Update Tools → Release Channel` روی **beta** بگذار.

سروری که به هیچ‌کدام دسترسی ندارد، [آفلاین نصب می‌کند](install.md).

</div>

---
[← Back to the docs index](README.md)

---

*Last verified against Backpack v2.0.0.*
