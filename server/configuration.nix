{
  config,
  lib,
  pkgs,
  inputs,
  modulesPath,
  ...
}:

let
  domain = "werewolf.simon-peleska.at";
  sshPubKeys = [
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOwpQ60GkyiUQzKvQXwx+TEVrJ6Gtyr81OXkEshRm/SW"
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHFqwByfThvVa8/np6/Ujrz0d6cb3RztwCbY78d25eRA simon@Framework"
  ];

  # Set this to the disk device shown in rescue mode (lsblk).
  # Typically /dev/sda on HDD/SSD servers, /dev/nvme0n1 on NVMe.
  disk = "/dev/sda";
in

{
  imports = [
    (modulesPath + "/profiles/qemu-guest.nix")
  ];

  nixpkgs.hostPlatform = lib.mkDefault "x86_64-linux";

  # ── Disk layout (disko) ────────────────────────────────────────────────────
  # nixos-anywhere uses this to partition and format the disk automatically.
  # GPT + 1 MiB BIOS boot partition (required for GRUB on GPT) + ext4 root.
  # https://wiki.nixos.org/wiki/Install_NixOS_on_Hetzner_Cloud
  disko.devices.disk.main = {
    type   = "disk";
    device = disk;
    content = {
      type = "gpt";
      partitions = {
        boot = {
          size = "1M";
          type = "EF02"; # BIOS boot partition — GRUB writes stage 1.5 here
          priority = 1;  # must be first on disk
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

  boot.loader.grub.enable = true; # device is set automatically by disko

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
    defaults.email = "";   # used for expiry notifications
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

  # ── Static networking (Hetzner Cloud) ─────────────────────────────────────
  # IPv4 is /32 on Hetzner — the gateway 172.31.1.1 is not in the same subnet,
  # so GatewayOnLink = true is required.
  # https://wiki.nixos.org/wiki/Install_NixOS_on_Hetzner_Cloud
  networking.useNetworkd = true;
  systemd.network.enable = true;
  systemd.network.networks."30-wan" = {
    matchConfig.Name = "ens3"; # ens3 on amd64; enp1s0 on arm64 — verify with `ip addr`
    networkConfig.DHCP = "no";
    address = [
      "178.104.5.193/32"
      "2a01:4f8:1c19:1d5a::1/64"
    ];
    routes = [
      { Gateway = "172.31.1.1"; GatewayOnLink = true; }
      { Gateway = "fe80::1"; }
    ];
  };

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
