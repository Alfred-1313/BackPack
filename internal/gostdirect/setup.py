"""Create one native GOST TCP forward. Iran dials the TLS relay on Kharej."""
import base64
import ipaddress
import json
import os
from pathlib import Path
import platform
import re
import secrets
import shutil
import socket
import subprocess
import sys
import time
from urllib.parse import quote, urlencode

def run(*args):
    return subprocess.run(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, check=True, timeout=25)

def port(value):
    n = int(value)
    if not 1 <= n <= 65535:
        raise ValueError("port must be 1-65535")
    return n

def wait_ready(unit, listen_port):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", listen_port), timeout=0.2):
                pass
            run("systemctl", "is-active", "--quiet", unit)
            return
        except OSError:
            time.sleep(0.15)
    raise ValueError(f"GOST did not start listening on TCP port {listen_port}")

def test_paths(name):
    if not re.fullmatch(r"bpct[a-f0-9]{12}", name):
        raise ValueError("invalid temporary GOST tunnel name")
    return (Path("/run/backpack-ct-gost") / name,
            Path("/run/systemd/system") / ("bp-gost-direct-" + name + ".service"))

def v2_config(join, host, endpoint, target, ingress, password, cert, key, temporary):
    credentials = "backpack:" + quote(password, safe="")
    destination = "127.0.0.1:" + str(target)
    if join:
        options = urlencode({"cert": str(cert), "key": str(key),
                             "whitelist": "tcp:" + destination})
        return {"ServeNodes": [f"relay+tls://{credentials}@:{endpoint}?{options}"]}
    options = urlencode({"secure": "true", "ca": str(cert), "nodelay": "true"})
    bind = "127.0.0.1" if temporary else "0.0.0.0"
    return {"ServeNodes": [f"tcp://{bind}:{ingress}/{destination}"],
            "ChainNodes": [f"relay+tls://{credentials}@{host}:{endpoint}?{options}"]}

def install(form):
    temporary = form.get("_test", False)
    join = form["action"] == "join"
    if join:
        text = form["token"].strip()
        prefix, _, payload = text.partition(".")
        if prefix not in ("bpgost1", "bpgost2") or len(payload) > 24000:
            raise ValueError("invalid GOST Direct setup token")
        form = json.loads(base64.b64decode(payload, validate=True))
        version = form.get("version", 3)
        if (prefix == "bpgost1" and version != 3) or (prefix == "bpgost2" and version != 2):
            raise ValueError("GOST setup token version does not match its selected core")
    name = form["name"]
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}", name):
        raise ValueError("use 1-32 letters, digits, underscores or hyphens for the name")
    host = str(ipaddress.IPv4Address(form["peer"]))
    endpoint, target = port(form["port"]), port(form["target"])
    ingress = 0 if join else port(form["ingress"])
    weight = int(form.get("cpuWeight", 0))
    restart = int(form.get("restartSeconds", 0))
    if not 0 <= weight <= 10000 or not 0 <= restart <= 60:
        raise ValueError("invalid GOST CPU weight or restart delay")
    arch = {"x86_64":"amd64","aarch64":"arm64"}[platform.machine()]
    version = form.get("version", 3)
    helpers = {2: ("gost-v2", "v2.12.0"), 3: ("gost", "v3.2.6")}
    if type(version) is not int or version not in helpers:
        raise ValueError("unsupported GOST version")
    helper, release = helpers[version]
    binary = Path("/usr/local/lib/backpack/helpers") / helper / (release+"-"+arch) / "gost"
    if not os.access(binary, os.X_OK):
        raise ValueError(f"install the pinned GOST {release} helper with BP_HELPERS={helper}")
    listen_port = endpoint if join else ingress
    listen_host = "127.0.0.1" if temporary and not join else "0.0.0.0"
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as candidate:
        candidate.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            candidate.bind((listen_host, listen_port))
        except OSError as err:
            raise ValueError(f"GOST TCP port {listen_port} is unavailable: {err.strerror}") from err
    directory = Path("/etc/backpack/gost-direct") / name
    unit = Path("/etc/systemd/system") / ("bp-gost-direct-"+name+".service")
    if temporary:
        directory, unit = test_paths(name)
    directory.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    directory.mkdir(mode=0o700)
    unit_created = False
    try:
        cert, key = directory/"cert.pem", directory/"key.pem"
        token = ""
        if join:
            password = form["password"]
            cert.write_text(form["certificate"])
            key.write_text(form["key"])
            if not re.fullmatch(r"[A-Za-z0-9_-]{32,96}",password):
                raise ValueError("invalid relay credential")
            pub = run("openssl","x509","-in",cert,"-pubkey","-noout").stdout
            if pub != run("openssl","pkey","-in",key,"-pubout").stdout:
                raise ValueError("relay certificate and key do not match")
            service = {"name":name, "addr":"0.0.0.0:"+str(endpoint),
                "listener":{"type":"tls","tls":{"certFile":str(cert),"keyFile":str(key)}},
                "handler":{"type":"relay","auth":{"username":"backpack","password":password},"metadata":{"bind":False}},
                "forwarder":{"nodes":[{"name":"target","addr":"127.0.0.1:"+str(target)}]}}
            config = {"services":[service]}
        else:
            password = form.get("secret") or secrets.token_urlsafe(36)
            if not re.fullmatch(r"[A-Za-z0-9_-]{32,96}", password):
                raise ValueError("invalid relay credential")
            run("openssl","req","-x509","-newkey","ec","-pkeyopt","ec_paramgen_curve:P-256","-nodes",
                "-days","365","-subj","/CN="+host,"-addext","subjectAltName=IP:"+host,"-keyout",key,"-out",cert)
            payload = dict(name=name,peer=host,port=endpoint,target=target,password=password,
                           certificate=cert.read_text(),key=key.read_text(),cpuWeight=weight,restartSeconds=restart)
            if version != 3:
                payload["version"] = version
            prefix = "bpgost1." if version == 3 else "bpgost2."
            token = prefix+base64.b64encode(json.dumps(payload).encode()).decode()
            key.unlink()
            node = {"name":"kharej","addr":host+":"+str(endpoint),
                "dialer":{"type":"tls","tls":{"secure":True,"serverName":host,"caFile":str(cert)}},
                "connector":{"type":"relay","auth":{"username":"backpack","password":password}}}
            service = {"name":name,"addr":("127.0.0.1:" if temporary else "0.0.0.0:")+str(ingress),"listener":{"type":"tcp"},
                "handler":{"type":"tcp","chain":"kharej"},
                "forwarder":{"nodes":[{"name":"target","addr":"127.0.0.1:"+str(target)}]}}
            config = {"services":[service],"chains":[{"name":"kharej","hops":[{"name":"relay","nodes":[node]}]}]}
        if version == 2:
            config = v2_config(join, host, endpoint, target, ingress, password, cert, key, temporary)
        cfg = directory/"gost.json"
        cfg.write_text(json.dumps(config))
        if token:
            (directory/"setup-token").write_text(token)
        with unit.open("x") as out:
            unit_created = True
            out.write("[Unit]\nDescription=BackPack GOST "+release+" Direct "+name+"\nAfter=network-online.target\n"
                      "[Service]\nExecStart="+str(binary)+" -C "+str(cfg)+"\n")
            if temporary:
                out.write("Restart=no\nTimeoutStopSec=5\nRuntimeMaxSec=90min\n"
                          "LogRateLimitIntervalSec=1min\nLogRateLimitBurst=10\n")
            else:
                out.write("Restart=on-failure\nRestartSec="+str(restart or 3)+"\n")
                if weight:
                    out.write("CPUWeight="+str(weight)+"\n")
            out.write("[Install]\nWantedBy=multi-user.target\n")
        run("systemctl","daemon-reload")
        if temporary:
            run("systemctl","start",unit.name)
        else:
            run("systemctl","enable","--now",unit.name)
        wait_ready(unit.name, listen_port)
        return {"token":token, "unit":unit.name}
    except BaseException:
        if unit_created:
            subprocess.run((["systemctl","stop",unit.name] if temporary else ["systemctl","disable","--now",unit.name]),capture_output=True)
            unit.unlink(missing_ok=True)
            subprocess.run(["systemctl","daemon-reload"],capture_output=True)
        shutil.rmtree(directory)
        raise

if __name__ == "__main__":
    os.umask(0o077)
    try:
        form = json.load(sys.stdin)
        if form.get("_test") and form["action"] == "remove":
            directory, unit = test_paths(form["name"])
            if unit.exists():
                run("systemctl", "stop", unit.name)
                unit.unlink()
                run("systemctl", "daemon-reload")
            if directory.exists():
                shutil.rmtree(directory)
            print("{}")
        else:
            print(json.dumps(install(form)))
    except subprocess.CalledProcessError as err:
        sys.exit(err.cmd[0]+": "+(err.stderr.decode(errors="replace").strip() or "service did not become active"))
    except Exception as err:
        sys.exit(str(err))

