// pm2 ecosystem config for relay-bridge.
//
// There are two relay types (vercel / cloudflare) and they MUST NOT be mixed.
// Pick exactly one via RELAY_TYPE; only the *_LIST matching it is loaded at
// runtime. Both lists are declared here so you can flip RELAY_TYPE to switch
// without editing the lists.
//
//   pm2 start ecosystem.config.js
//   # switch type after editing RELAY_TYPE below:
//   pm2 restart relay-bridge --update-env
//
// To use the -relay single mode instead, pass it in `args` and it overrides env.

module.exports = {
  apps: [
    {
      name: "relay-bridge",
      script: "./relay-bridge",
      cwd: __dirname,
      instances: 1,
      exec_mode: "fork",
      autorestart: true,
      max_memory_restart: "150M",
      // -relay intentionally omitted so the pool is built from the env below.
      args: "-http 127.0.0.1:8080 -socks 127.0.0.1:1080",
      env: {
        // ---- select ONE type: "vercel" or "cloudflare" ----
        RELAY_TYPE: "vercel",

        // Vercel relays (comma-separated)
        RELAY_VERCEL:
          "https://relay-deployer-santo-4xl0o68gv-santonijaya8-4344s-projects.vercel.app," +
          "https://relay-deployer-auah-fp850hnlh-auah-s-projects.vercel.app," +
          "https://relay-deployer-sengkyou-egw1q82ao-goodboyguds-projects.vercel.app",

        // Cloudflare workers relays (comma-separated)
        RELAY_CLOUDFLARE:
          "https://aurora8456-inbox-nullpter-web-id.a8310d927dc.workers.dev," +
          "https://caleb05-student-nullpter-web-id.a746906ca1e.workers.dev," +
          "https://salma31-teach-nullpter-web-id.aebd1a703c8.workers.dev",

        // Optional secret, sent to the relay as x-relay-secret; empty = none.
        RELAY_SECRET: "",
      },
    },
  ],
};
