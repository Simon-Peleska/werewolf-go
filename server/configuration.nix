{
  config,
  pkgs,
  inputs,
  ...
}:

# local.nix is gitignored — copy local.nix.example and fill in your values.
# It must return an attrset with: domain, acmeEmail, flakeUrl, sshPubKeys.
let
  local = import ./local.nix;
  inherit (local) domain sshPubKeys;

  # Set this to the disk device shown in rescue mode (lsblk).
  # Typically /dev/sda on HDD/SSD servers, /dev/nvme0n1 on NVMe.
  disk = "/dev/sda";
in

{
  # ── Disk layout (disko) ────────────────────────────────────────────────────
  # nixos-anywhere uses this to partition and format the disk automatically.
  # GPT + EFI boot + ext4 root. Simple and works on all Hetzner server types.
  disko.devices.disk.main = {
    type   = "disk";
    device = disk;
    content = {
      type = "gpt";
      partitions = {
        boot = {
          size = "512M";
          type = "EF00";  # EFI System Partition
          content = {
            type   = "filesystem";
            format = "vfat";
            mountpoint = "/boot";
          };
        };
        root = {
          size = "100%";
          content = {
            type   = "filesystem";
            format = "ext4";
            mountpoint = "/";
          };
        };
      };
    };
  };

  boot.loader.systemd-boot.enable      = true;
  boot.loader.efi.canTouchEfiVariables = true;

  # ── Werewolf service ───────────────────────────────────────────────────────
  # Secrets and config live in /etc/werewolf/config.json on the server.
  # Create it manually — it is never committed or part of the Nix store.
  # Example:
  #   {
  #     "storyteller_provider": "openai",
  #     "storyteller_model": "gpt-4o-mini",
  #     "storyteller_api_key": "sk-...",
  #     "narrator_api_key": "sk-..."
  #   }
  # Permissions:
  #   chown root:werewolf /etc/werewolf/config.json && chmod 640 /etc/werewolf/config.json
  services.werewolf = {
    enable = true;
    package = inputs.werewolf.packages.x86_64-linux.default;
    listenAddr = "127.0.0.1:8080";
    # configFile defaults to /etc/werewolf/config.json — no need to set it here.
  };

  # ── nginx + HTTPS ──────────────────────────────────────────────────────────
  security.acme = {
    acceptTerms = true;
    # defaults.email = acmeEmail;   # used for expiry notifications
  };

  services.nginx = {
    enable = true;
    recommendedProxySettings = true;
    recommendedTlsSettings = true;
    recommendedGzipSettings = true;

    virtualHosts.${domain} = {
      enableACME = true; # NixOS automatically renews via systemd timer
      forceSSL = true;

      locations."/" = {
        proxyPass = "http://${config.services.werewolf.listenAddr}";
        proxyWebsockets = true; # game uses persistent WebSocket connections
        extraConfig = ''
          proxy_read_timeout 3600s;
          proxy_send_timeout 3600s;
        '';
      };
    };
  };

  networking.firewall.allowedTCPPorts = [
    80
    443
  ];

  # ── Automatic OS updates ───────────────────────────────────────────────────
  # Pulls the latest commit from this flake's GitHub repo and switches to it.
  # The werewolf version is pinned via flake.lock — run `nix flake update werewolf`
  # in this repo and push to deploy a new game version.
  system.autoUpgrade = {
    enable = true;
    flake = "github:simon-peleska/server";
    dates = "04:00"; # daily at 4 AM
    randomizedDelaySec = "1h"; # spread load if you run multiple servers
    allowReboot = true; # reboot automatically after kernel upgrades
  };

  # ── Machine basics ─────────────────────────────────────────────────────────
  networking.hostName = "server-1";

  time.timeZone = "UTC";

  services.openssh = {
    enable = true;
    settings.PasswordAuthentication = false;
    settings.PermitRootLogin = "no";
  };

  users.users.admin = {
    isNormalUser = true;
    extraGroups = [ "wheel" ];
    openssh.authorizedKeys.keys = sshPubKeys;
  };

  # Allow `admin` to run sudo without a password (optional — remove if you prefer typed sudo).
  security.sudo.wheelNeedsPassword = false;

  system.stateVersion = "25.05";
}
