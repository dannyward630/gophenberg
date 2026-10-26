Feature: Check a Linux server before installing Gophenberg
  sudo gophenberg server:install sets up Gophenberg on a fresh Ubuntu
  24.04, Debian 12 or Debian 13 server. Before it changes anything it
  reads its settings, reads its install record and checks the server:
  root, the system, systemd and the sandbox of the site's service, memory,
  disk, the ports, any PostgreSQL or Caddy it did not install, the folders
  above the install root, and that the domain points at this server. A
  check that fails stops the run with a line that says why and that
  nothing was changed. A later run skips the ports and the pieces the
  install record says an earlier run installed. With -dry-run the run then
  prints each step it would take and the full text of every file it would
  write, and still changes nothing. Root never takes a setting from the
  site's .env, since the site's own user can write it. It reads only the
  five stop graces from it, as plain numbers, for the time systemd gives
  the site to stop, and on a server that keeps its own web server the
  upload cap. With -web-server none the server keeps a web server of its
  own: the run leaves out Caddy, the ports Caddy answers on and the
  domain check, and its last step prints the lines that web server needs
  to send the domain to the site. A later run never moves a server from
  one web server to the other.

  Background:
    Given a fresh Ubuntu 24.04 server run by systemd 255

  Scenario: A plain user is refused
    Given the operator is not root
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "server:install must run as root, such as with sudo, so nothing was changed"
    And nothing on the server changed

  Scenario: A plain user is refused on a server an earlier run set up
    Given an earlier run installed Gophenberg for example.com on the server
    And only root may read the server settings and the install record
    And the operator is not root
    When the operator runs "server:install -dry-run"
    Then the run stops with "server:install must run as root, such as with sudo, so nothing was changed"
    And nothing on the server changed

  Scenario: A system the installer does not set up is refused
    Given the server runs Ubuntu 22.04
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "this server runs Ubuntu 22.04.5 LTS, and server:install sets up Ubuntu 24.04, Debian 12 and Debian 13 only, so nothing was changed"
    And nothing on the server changed

  Scenario: A systemd older than 251 is refused
    Given the server runs systemd 250
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "this server runs systemd 250, and the site's units need systemd 251 or newer, so nothing was changed"
    And nothing on the server changed

  Scenario: A server where systemd cannot set up the sandbox of the site's service is refused
    Given systemd cannot set up a sandbox on the server, a container systemd-detect-virt reports as lxc
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "systemd cannot set up the sandbox the site's service runs in on this server, as on a container plan without nesting, and systemd-detect-virt reports lxc, so nothing was changed"
    And nothing on the server changed

  Scenario: A server with too little memory is refused
    Given the server has 512 MB of memory
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "this server has 512 MB of memory, less than the 900 MB GOPHENBERG_INSTALL_MEMORY_FLOOR_MB asks, so nothing was changed"
    And nothing on the server changed

  Scenario: A server where something listens on port 80 is refused
    Given something listens on TCP port 80
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "something already listens on TCP port 80, which Caddy answers on, so nothing was changed"
    And nothing on the server changed

  Scenario: A site address on a port Caddy will answer on is refused
    Given the environment sets GOPHENBERG_ADDR to 127.0.0.1:443
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "127.0.0.1:443, the GOPHENBERG_ADDR the site listens on, takes TCP port 443, which Caddy answers on, and GOPHENBERG_ADDR must name another port, in the environment or in its line in /etc/gophenberg/server.env, so nothing was changed"
    And the checks listened on no port
    And nothing on the server changed

  Scenario: A server that keeps its own web server skips the web server checks
    Given something listens on TCP port 80
    And Caddy is installed on the server
    And example.com has no A or AAAA record
    When the operator runs "server:install -dry-run -domain example.com -web-server none"
    Then the run succeeds
    And the run notes nothing
    And the checks listened only on 127.0.0.1:8081 and TCP port 5432
    And the checks looked up no domain
    And nothing on the server changed

  Scenario: A later run that asks for the other web server is refused
    Given an earlier run installed Gophenberg for example.com on the server
    When the operator runs "server:install -dry-run -web-server none"
    Then the run stops with "GOPHENBERG_INSTALL_WEB_SERVER: this server was set up with caddy, and a later run cannot move it to none, so run sudo gophenberg server:uninstall first, then change the line in /etc/gophenberg/server.env"
    And nothing on the server changed

  Scenario: A later run whose server settings were changed by hand to the other web server is refused
    Given an earlier run installed Gophenberg for example.com on the server
    And the web server line of the server settings was changed by hand to none
    When the operator runs "server:install -dry-run"
    Then the run stops with "GOPHENBERG_INSTALL_WEB_SERVER: this server was set up with caddy, and a later run cannot move it to none, so run sudo gophenberg server:uninstall first, then change the line in /etc/gophenberg/server.env"
    And nothing on the server changed

  Scenario: A domain with no address is refused
    Given example.com has no A or AAAA record
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "cannot find an A or AAAA record of example.com, which must point at this server: lookup example.com: no such host, so nothing was changed"
    And the run notes "the hosting provider's firewall must let TCP 80, TCP 443 and UDP 443 in for HTTPS to work, and this check cannot see it"
    And nothing on the server changed

  Scenario: A domain that points at another server is refused
    Given example.com points at 198.51.100.7, where another server answers every page with 404
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "example.com points at 198.51.100.7, which did not answer the check as this server would (it answered 404 Not Found), likely because example.com points at another server or because the network does not route a request from this server to its own public address back to it, and -skip-domain-check skips this check for a server behind a proxy it cannot cross, so nothing was changed"
    And the run notes "the hosting provider's firewall must let TCP 80, TCP 443 and UDP 443 in for HTTPS to work, and this check cannot see it"
    And nothing on the server changed

  Scenario: A domain that resolves to a loopback address is refused
    Given example.com resolves to 127.0.1.1, as a line in /etc/hosts makes it
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "example.com resolves to 127.0.1.1, an address that never leaves this server, likely from a line in /etc/hosts, and Let's Encrypt asks for the domain from outside, which needs its public A or AAAA record to point at this server, and -skip-domain-check skips this check once it does, so nothing was changed"
    And the run notes "the hosting provider's firewall must let TCP 80, TCP 443 and UDP 443 in for HTTPS to work, and this check cannot see it"
    And the checks fetched no page
    And nothing on the server changed

  Scenario: A later run for a new domain behind NAT is refused while the Caddy an earlier run installed holds port 80
    Given an earlier run installed Gophenberg for example.com on the server
    And www.example.com points at 198.51.100.7, a public address a NAT in front of the server forwards to it
    When the operator runs "server:install -dry-run -domain www.example.com"
    Then the run stops with "the Caddy this installer set up holds TCP port 80, so the check that www.example.com points at this server cannot run, and -skip-domain-check skips it once the domain points here, so nothing was changed"
    And the run notes "the hosting provider's firewall must let TCP 80, TCP 443 and UDP 443 in for HTTPS to work, and this check cannot see it"
    And the checks fetched no page
    And nothing on the server changed

  Scenario: A domain that is not a host name is refused before the server is read
    When the operator runs "server:install -dry-run -domain Example.com"
    Then the run stops with "-domain: must be localhost or a host name such as example.com: labels of 1 to 63 lower-case letters, digits and hyphens joined by dots, no hyphen at either end of a label, a letter in the last label, 253 characters at most, got "Example.com""
    And nothing on the server changed

  Scenario: An archive outside the installer's folder is refused before the server is read
    When the operator runs "server:install -dry-run -domain example.com -archive /tmp/gophenberg_0.20.0_linux_amd64.tar.gz"
    Then the run stops with "-archive: must be a file inside /var/lib/gophenberg-installer, the folder install.sh downloads it into, got "/tmp/gophenberg_0.20.0_linux_amd64.tar.gz""
    And nothing on the server changed

  Scenario: A domain whose last label holds no letter is refused before the server is read
    When the operator runs "server:install -dry-run -domain 203.0.113.7"
    Then the run stops with "-domain: must be localhost or a host name such as example.com: labels of 1 to 63 lower-case letters, digits and hyphens joined by dots, no hyphen at either end of a label, a letter in the last label, 253 characters at most, got "203.0.113.7""
    And nothing on the server changed

  Scenario: A server that already runs PostgreSQL is refused
    Given PostgreSQL is installed on the server
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "this server already has PostgreSQL at /etc/postgresql, and server:install sets up a fresh server only, so nothing was changed"
    And nothing on the server changed

  Scenario: An install root under a folder another user can write is refused
    Given another user can write /opt
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "/opt, a parent of the install root /opt/gophenberg, must be owned by root and writable by root alone, so nothing was changed"
    And nothing on the server changed

  Scenario: An install root that would hand the site's user a folder root trusts is refused
    Given the environment sets GOPHENBERG_INSTALL_ROOT to /usr/local
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops with "the install root /usr/local would hand /usr/local/lib/gophenberg, whose files root runs or reads, to the site's user, and GOPHENBERG_INSTALL_ROOT must name another folder, so nothing was changed"
    And nothing on the server changed

  Scenario: An installer older than the one that set up the server is refused
    Given the install record names the installer "1000.0.0"
    When the operator runs "server:install -dry-run -domain example.com"
    Then the run stops because this installer is older than the 1000.0.0 installer
    And nothing on the server changed

  Scenario: A later run skips the ports and the pieces an earlier run installed
    Given an earlier run installed Gophenberg for example.com on the server
    When the operator runs "server:install -dry-run"
    Then the run succeeds
    And the checks listened on no port
    And nothing on the server changed
    And the dry run lists these steps:
      """
      Step 1: start the install record /var/lib/gophenberg-installer/install.json and write the server settings
      Step 2: install PostgreSQL and Caddy with apt-get, Caddy from its own apt repository
      Step 3: make the system user gophenberg, which runs the site and signs in to PostgreSQL
      Step 4: make the install root /opt/gophenberg and its folders, owned by gophenberg
      Step 5: make the database role gophenberg and its database gophenberg as the postgres user
      Step 6: keep the build /opt/gophenberg/current names, since updates go through update:apply
      Step 7: keep a copy of this installer at /usr/local/lib/gophenberg/gophenberg, the one root runs for server:install and server:uninstall
      Step 8: write the site's settings, then run check as gophenberg in /opt/gophenberg
      Step 9: write the systemd units and Caddy's drop-in, then reload systemd and enable gophenberg.service
      Step 10: write the gophenberg command on the path
      Step 11: write Caddy's files, check them with caddy validate and reload Caddy
      Step 12: restart gophenberg.service and wait until /admin/, /api/locale and / answer on 127.0.0.1:8081
      Step 13: check that https://example.com/admin/ answers within 2m0s
      Step 14: print the summary and end the install record
      """

  Scenario: A GOPHENBERG_INSTALL_ROOT line planted in the site's .env is never read
    Given an earlier run installed Gophenberg for example.com on the server
    And the site's .env holds the line "GOPHENBERG_INSTALL_ROOT=/etc"
    And the site's .env holds the line "GOPHENBERG_SITE_TITLE=Maria Perez's site"
    And the site's .env holds the line "GOPHENBERG_SHUTDOWN_GRACE=4m"
    When the operator runs "server:install -dry-run"
    Then the run succeeds
    And nothing on the server changed
    And the dry run writes "/opt/gophenberg/.env", owned by gophenberg with mode 0600:
      """
      # Written by gophenberg server:install.
      GOPHENBERG_DATABASE_URL=postgres:///gophenberg?host=/var/run/postgresql&user=gophenberg
      GOPHENBERG_SUPERUSER_POLICY=refuse
      GOPHENBERG_INSTALL_ROOT=/opt/gophenberg
      GOPHENBERG_ADDR=127.0.0.1:8081
      GOPHENBERG_PUBLIC_URL=https://example.com
      GOPHENBERG_TRUSTED_PROXIES=127.0.0.1/32,::1/128
      GOPHENBERG_THEMES_DIR=/opt/gophenberg/themes
      GOPHENBERG_MEDIA_DIR=/opt/gophenberg/media
      GOPHENBERG_DATABASE_WAIT=60s
      GOPHENBERG_SHUTDOWN_GRACE=4m
      GOPHENBERG_SITE_TITLE="Maria Perez's site"
      """
    And the site's unit holds the line "WorkingDirectory=/opt/gophenberg"
    And the site's unit holds the line "ReadWritePaths=/opt/gophenberg"
    And the site's unit holds the line "TimeoutStopSec=260s"

  Scenario: A line of the site's .env that holds a terminal control sequence is refused
    Given an earlier run installed Gophenberg for example.com on the server
    And the site's .env holds the line "GOPHENBERG_SITE_TITLE=Maria<ESC>[2JPerez", <ESC> standing for the escape character
    When the operator runs "server:install -dry-run"
    Then the run stops with "installer: /opt/gophenberg/.env: "GOPHENBERG_SITE_TITLE" holds a control character or a byte that is not UTF-8, which no setting needs"
    And the run prints nothing
    And nothing on the server changed

  Scenario Outline: The site's unit restarts directly only where systemd knows how
    Given the server runs <system> with systemd <systemd>
    When the operator runs "server:install -dry-run -domain example.com -admin-email admin@example.com"
    Then the run succeeds
    And the site's unit <holds> the line "RestartMode=direct"

    Examples:
      | system       | systemd | holds |
      | Ubuntu 24.04 | 255     | holds |
      | Debian 12    | 252     | lacks |

  Scenario: A dry run prints every step and the full text of every file for a real domain
    Given unattended-upgrades is installed on the server
    When the operator runs "server:install -dry-run -domain example.com -admin-email admin@example.com"
    Then the run succeeds
    And the run notes "the hosting provider's firewall must let TCP 80, TCP 443 and UDP 443 in for HTTPS to work, and this check cannot see it"
    And nothing on the server changed
    And the dry run lists these steps:
      """
      Step 1: start the install record /var/lib/gophenberg-installer/install.json and write the server settings
      Step 2: install PostgreSQL and Caddy with apt-get, Caddy from its own apt repository
      Step 3: make the system user gophenberg, which runs the site and signs in to PostgreSQL
      Step 4: make the install root /opt/gophenberg and its folders, owned by gophenberg
      Step 5: make the database role gophenberg and its database gophenberg as the postgres user
      Step 6: unpack the release archive into /opt/gophenberg/versions and point /opt/gophenberg/current at it
      Step 7: keep a copy of this installer at /usr/local/lib/gophenberg/gophenberg, the one root runs for server:install and server:uninstall
      Step 8: write the site's settings, then run check as gophenberg in /opt/gophenberg
      Step 9: write the systemd units and Caddy's drop-in, then reload systemd and enable gophenberg.service
      Step 10: write the gophenberg command on the path
      Step 11: write Caddy's files, keep the package's Caddyfile as /etc/caddy/Caddyfile.before-gophenberg, check them with caddy validate and restart Caddy
      Step 12: start gophenberg.service and wait until /admin/, /api/locale and / answer on 127.0.0.1:8081
      Step 13: create the first administrator with account:create-admin as gophenberg
      Step 14: check that https://example.com/admin/ answers within 2m0s
      Step 15: print the summary and end the install record
      """
    And the dry run writes "/etc/gophenberg/server.env", owned by root with mode 0600:
      """
      # Written by gophenberg server:install.
      GOPHENBERG_INSTALL_ROOT=/opt/gophenberg
      GOPHENBERG_PUBLIC_URL=https://example.com
      GOPHENBERG_ADDR=127.0.0.1:8081
      GOPHENBERG_INSTALL_WEB_SERVER=caddy
      GOPHENBERG_INSTALL_CERTIFICATE_EMAIL=admin@example.com
      GOPHENBERG_SERVICE_RESTART_DELAY=5s
      GOPHENBERG_SERVICE_START_LIMIT_INTERVAL=300s
      GOPHENBERG_SERVICE_START_LIMIT_BURST=10
      GOPHENBERG_SERVICE_STOP_MARGIN=5s
      GOPHENBERG_MAINTENANCE_RETRY_AFTER=600s
      GOPHENBERG_PROXY_RETRY_DURATION=10s
      """
    And the dry run writes "/etc/apt/keyrings/caddy.asc", owned by root with mode 0644, holding Caddy's repository key
    And the dry run writes "/etc/apt/sources.list.d/caddy.sources", owned by root with mode 0644:
      """
      # Written by gophenberg server:install.
      Types: deb
      URIs: https://dl.cloudsmith.io/public/caddy/stable/deb/debian
      Suites: any-version
      Components: main
      Signed-By: /etc/apt/keyrings/caddy.asc
      """
    And the dry run writes "/etc/apt/preferences.d/gophenberg-caddy", owned by root with mode 0644:
      """
      Explanation: Written by gophenberg server:install.
      Package: caddy
      Pin: origin dl.cloudsmith.io
      Pin-Priority: 500

      Package: *
      Pin: origin dl.cloudsmith.io
      Pin-Priority: -1
      """
    And the dry run writes "/etc/apt/apt.conf.d/52gophenberg-caddy", owned by root with mode 0644:
      """
      # Written by gophenberg server:install.
      Unattended-Upgrade::Origins-Pattern {
      	"origin=cloudsmith/caddy/stable,site=dl.cloudsmith.io";
      };
      """
    And the dry run writes "/opt/gophenberg/.env", owned by gophenberg with mode 0600:
      """
      # Written by gophenberg server:install.
      GOPHENBERG_DATABASE_URL=postgres:///gophenberg?host=/var/run/postgresql&user=gophenberg
      GOPHENBERG_SUPERUSER_POLICY=refuse
      GOPHENBERG_INSTALL_ROOT=/opt/gophenberg
      GOPHENBERG_ADDR=127.0.0.1:8081
      GOPHENBERG_PUBLIC_URL=https://example.com
      GOPHENBERG_TRUSTED_PROXIES=127.0.0.1/32,::1/128
      GOPHENBERG_THEMES_DIR=/opt/gophenberg/themes
      GOPHENBERG_MEDIA_DIR=/opt/gophenberg/media
      GOPHENBERG_DATABASE_WAIT=60s
      """
    And the dry run writes "/etc/systemd/system/gophenberg.service", owned by root with mode 0644:
      """
      # Written by gophenberg server:install.
      [Unit]
      Description=Gophenberg
      Documentation=https://docs.gophenberg.org/self-hosting/server-install/
      After=network-online.target postgresql.service
      Wants=network-online.target
      StartLimitIntervalSec=300
      StartLimitBurst=10
      OnFailure=gophenberg-rollback.service

      [Service]
      Type=exec
      User=gophenberg
      Group=gophenberg
      WorkingDirectory=/opt/gophenberg
      ExecStart=/opt/gophenberg/current/gophenberg serve
      Restart=on-failure
      RestartMode=direct
      RestartSec=5s
      SuccessExitStatus=75
      RestartForceExitStatus=75
      RestartPreventExitStatus=76
      TimeoutStopSec=30s
      KillMode=control-group
      NoNewPrivileges=true
      ProtectSystem=strict
      ReadWritePaths=/opt/gophenberg
      PrivateTmp=yes

      [Install]
      WantedBy=multi-user.target
      """
    And the dry run writes "/etc/systemd/system/gophenberg-rollback.service", owned by root with mode 0644:
      """
      # Written by gophenberg server:install.
      [Unit]
      Description=Roll Gophenberg back after a failed update
      Documentation=https://docs.gophenberg.org/self-hosting/updates-and-backups/#a-version-that-fails-its-check
      ConditionPathExists=/opt/gophenberg/previous
      StartLimitIntervalSec=0

      [Service]
      Type=oneshot
      User=gophenberg
      Group=gophenberg
      WorkingDirectory=/opt/gophenberg
      ExecStart=/opt/gophenberg/previous/gophenberg update:rollback -after-failure
      ExecStartPost=+/usr/bin/systemctl reset-failed gophenberg.service
      ExecStartPost=+/usr/bin/systemctl start gophenberg.service
      NoNewPrivileges=true
      ProtectSystem=strict
      ReadWritePaths=/opt/gophenberg
      PrivateTmp=yes
      """
    And the dry run writes "/etc/systemd/system/caddy.service.d/gophenberg.conf", owned by root with mode 0644:
      """
      # Written by gophenberg server:install.
      [Service]
      RuntimeDirectory=caddy
      RuntimeDirectoryMode=0700
      """
    And the dry run writes "/usr/local/bin/gophenberg", owned by root with mode 0755:
      """
      #!/bin/sh
      # Written by gophenberg server:install.
      if [ "$(id -u)" -ne 0 ]; then
      	echo "gophenberg: run it with sudo, such as sudo gophenberg update:status" >&2
      	exit 1
      fi
      case "${1-}" in
      server:install | server:uninstall)
      	cd / || exit 1
      	exec /usr/local/lib/gophenberg/gophenberg "$@"
      	;;
      esac
      cd /opt/gophenberg || exit 1
      /usr/sbin/runuser --pty -u gophenberg -- /opt/gophenberg/current/gophenberg "$@"
      code=$?
      if [ "$code" -ne 75 ]; then
      	exit "$code"
      fi
      /usr/bin/systemctl reset-failed gophenberg.service
      /usr/bin/systemctl restart gophenberg.service || exit 1
      echo "gophenberg: restarted the site to start the new build"
      """
    And the dry run writes "/usr/local/share/gophenberg/maintenance/index.html", owned by root with mode 0644:
      """
      <!DOCTYPE html>
      <html lang="en">
      <head>
      	<meta charset="utf-8">
      	<meta name="viewport" content="width=device-width, initial-scale=1">
      	<title>Briefly unavailable</title>
      	<style>
      		body {
      			margin: 0;
      			padding: 48px 16px;
      			background: #fcfcfc;
      			color: #1e1e1e;
      			font-family: system-ui, sans-serif;
      		}
      		main {
      			box-sizing: border-box;
      			max-width: 700px;
      			margin: 0 auto;
      			padding: 24px 32px;
      			border: 1px solid #dbdbdb;
      			border-radius: 8px;
      			background: #fff;
      		}
      		p {
      			margin: 0;
      			font-size: 16px;
      			line-height: 1.5;
      		}
      	</style>
      </head>
      <body>
      	<main>
      		<p>This site is briefly unavailable. Check back in a minute.</p>
      	</main>
      </body>
      </html>
      """
    And the dry run writes "/etc/caddy/gophenberg.caddy", owned by root with mode 0644:
      """
      # Written by gophenberg server:install.
      example.com {
      	reverse_proxy 127.0.0.1:8081 {
      		lb_try_duration 10s
      	}
      	handle_errors 502 503 {
      		root * /usr/local/share/gophenberg/maintenance
      		rewrite * /index.html
      		header Retry-After 600
      		file_server {
      			status 503
      		}
      	}
      }
      """
    And the dry run writes "/etc/caddy/Caddyfile", owned by root with mode 0644:
      """
      # Written by gophenberg server:install on its first run.
      {
      	email admin@example.com
      	admin unix//run/caddy/admin.sock
      }

      import /etc/caddy/gophenberg.caddy
      """
    And the dry run writes no other file

  Scenario: A dry run for localhost names no certificate email
    Given unattended-upgrades is installed on the server
    When the operator runs "server:install -dry-run -domain localhost -admin-email admin@example.com"
    Then the run succeeds
    And the run notes nothing
    And nothing on the server changed
    And the dry run writes "/etc/gophenberg/server.env", owned by root with mode 0600:
      """
      # Written by gophenberg server:install.
      GOPHENBERG_INSTALL_ROOT=/opt/gophenberg
      GOPHENBERG_PUBLIC_URL=https://localhost
      GOPHENBERG_ADDR=127.0.0.1:8081
      GOPHENBERG_INSTALL_WEB_SERVER=caddy
      GOPHENBERG_INSTALL_CERTIFICATE_EMAIL=admin@example.com
      GOPHENBERG_SERVICE_RESTART_DELAY=5s
      GOPHENBERG_SERVICE_START_LIMIT_INTERVAL=300s
      GOPHENBERG_SERVICE_START_LIMIT_BURST=10
      GOPHENBERG_SERVICE_STOP_MARGIN=5s
      GOPHENBERG_MAINTENANCE_RETRY_AFTER=600s
      GOPHENBERG_PROXY_RETRY_DURATION=10s
      """
    And the dry run writes "/opt/gophenberg/.env", owned by gophenberg with mode 0600:
      """
      # Written by gophenberg server:install.
      GOPHENBERG_DATABASE_URL=postgres:///gophenberg?host=/var/run/postgresql&user=gophenberg
      GOPHENBERG_SUPERUSER_POLICY=refuse
      GOPHENBERG_INSTALL_ROOT=/opt/gophenberg
      GOPHENBERG_ADDR=127.0.0.1:8081
      GOPHENBERG_PUBLIC_URL=https://localhost
      GOPHENBERG_TRUSTED_PROXIES=127.0.0.1/32,::1/128
      GOPHENBERG_THEMES_DIR=/opt/gophenberg/themes
      GOPHENBERG_MEDIA_DIR=/opt/gophenberg/media
      GOPHENBERG_DATABASE_WAIT=60s
      """
    And the dry run writes "/etc/caddy/gophenberg.caddy", owned by root with mode 0644:
      """
      # Written by gophenberg server:install.
      localhost {
      	reverse_proxy 127.0.0.1:8081 {
      		lb_try_duration 10s
      	}
      	handle_errors 502 503 {
      		root * /usr/local/share/gophenberg/maintenance
      		rewrite * /index.html
      		header Retry-After 600
      		file_server {
      			status 503
      		}
      	}
      }
      """
    And the dry run writes "/etc/caddy/Caddyfile", owned by root with mode 0644:
      """
      # Written by gophenberg server:install on its first run.
      {
      	admin unix//run/caddy/admin.sock
      }

      import /etc/caddy/gophenberg.caddy
      """
    And the dry run writes every other file as it does for example.com

  Scenario: -dry-run with -web-server none prints the lines for the person's web server
    When the operator runs "server:install -dry-run -domain example.com -admin-email admin@example.com -web-server none"
    Then the run succeeds
    And the run notes nothing
    And nothing on the server changed
    And the dry run lists these steps:
      """
      Step 1: start the install record /var/lib/gophenberg-installer/install.json and write the server settings
      Step 2: install PostgreSQL with apt-get
      Step 3: make the system user gophenberg, which runs the site and signs in to PostgreSQL
      Step 4: make the install root /opt/gophenberg and its folders, owned by gophenberg
      Step 5: make the database role gophenberg and its database gophenberg as the postgres user
      Step 6: unpack the release archive into /opt/gophenberg/versions and point /opt/gophenberg/current at it
      Step 7: keep a copy of this installer at /usr/local/lib/gophenberg/gophenberg, the one root runs for server:install and server:uninstall
      Step 8: write the site's settings, then run check as gophenberg in /opt/gophenberg
      Step 9: write the systemd units, then reload systemd and enable gophenberg.service
      Step 10: write the gophenberg command on the path
      Step 11: start gophenberg.service and wait until /admin/, /api/locale and / answer on 127.0.0.1:8081
      Step 12: create the first administrator with account:create-admin as gophenberg
      Step 13: print the summary and end the install record
      Step 14: print the lines for nginx, Caddy, Traefik and Apache that send example.com to the site at 127.0.0.1:8081
      """
    And the dry run writes "/etc/gophenberg/server.env", owned by root with mode 0600:
      """
      # Written by gophenberg server:install.
      GOPHENBERG_INSTALL_ROOT=/opt/gophenberg
      GOPHENBERG_PUBLIC_URL=https://example.com
      GOPHENBERG_ADDR=127.0.0.1:8081
      GOPHENBERG_INSTALL_WEB_SERVER=none
      GOPHENBERG_INSTALL_CERTIFICATE_EMAIL=admin@example.com
      GOPHENBERG_SERVICE_RESTART_DELAY=5s
      GOPHENBERG_SERVICE_START_LIMIT_INTERVAL=300s
      GOPHENBERG_SERVICE_START_LIMIT_BURST=10
      GOPHENBERG_SERVICE_STOP_MARGIN=5s
      GOPHENBERG_MAINTENANCE_RETRY_AFTER=600s
      GOPHENBERG_PROXY_RETRY_DURATION=10s
      """
    And the dry run writes only these files:
      """
      /etc/gophenberg/server.env
      /opt/gophenberg/.env
      /etc/systemd/system/gophenberg.service
      /etc/systemd/system/gophenberg-rollback.service
      /usr/local/bin/gophenberg
      """
    And the dry run prints for the person's web server:
      """
      Your own web server provides HTTPS for example.com and must run on this server, since the
      site answers on 127.0.0.1:8081 alone. During the restart after an update it shows its own
      error page, unless it can hold requests until the site answers again. A web server in a
      container needs this server's own network, such as network_mode: host, since a
      container's loopback address is its own. The lines below send example.com to the site with
      the visitor's host, client address and scheme, and a body limit as large as the largest
      upload the site takes: a file of the 128 MB GOPHENBERG_MEDIA_UPLOAD_CAP_MB allows or a
      theme of 64 MB, each with 64 KB of form framing around it.

      nginx, in the server block that serves example.com over HTTPS:

      client_max_body_size 131136k;

      location / {
      	proxy_pass http://127.0.0.1:8081;
      	proxy_set_header Host $host;
      	proxy_set_header X-Forwarded-Host $host;
      	proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      	proxy_set_header X-Forwarded-Proto $scheme;
      }

      Caddy, in its Caddyfile, where reverse_proxy sends the visitor's host, client address
      and scheme on its own, and Caddy sets no upload limit:

      example.com {
      	reverse_proxy 127.0.0.1:8081
      }

      Traefik, in a file its file provider reads, where websecure and letsencrypt stand for
      your HTTPS entry point and certificate resolver, and Traefik sends the visitor's host,
      client address and scheme on its own and sets no size limit:

      http:
        routers:
          gophenberg:
            rule: Host(`example.com`)
            entryPoints:
              - websecure
            tls:
              certResolver: letsencrypt
            service: gophenberg
        services:
          gophenberg:
            loadBalancer:
              servers:
                - url: http://127.0.0.1:8081

      Traefik 3 cuts a request it has not read in full within the entry point's
      transport.respondingTimeouts.readTimeout, 60s by default. For large uploads, raise it in
      Traefik's install configuration to at least GOPHENBERG_UPLOAD_TIMEOUT, 5m0s unless the
      site's .env sets it, or to 0.

      Apache, in the virtual host that serves example.com over HTTPS, with its proxy_http and
      headers modules enabled:

      LimitRequestBody 134283264
      ProxyAddHeaders Off
      ProxyPreserveHost On
      RequestHeader set X-Forwarded-Host expr=%{HTTP_HOST}
      RequestHeader set X-Forwarded-For expr=%{REMOTE_ADDR}
      RequestHeader set X-Forwarded-Proto expr=%{REQUEST_SCHEME}
      ProxyPass / http://127.0.0.1:8081/
      ProxyPassReverse / http://127.0.0.1:8081/
      """
