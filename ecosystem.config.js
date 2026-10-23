module.exports = {
  apps: [
    {
      name: "relay-bridge",
      script: "/home/zidan/projects/relay-bridge/relay-bridge",
      args: "-http 127.0.0.1:8880 -socks 127.0.0.1:1180 -mitm",
      exec_mode: "fork",
      instances: 1,
      autorestart: true,
      env: {
        RELAY_TYPE: "all",
        RELAY_MODE: "fallback",
        RELAY_VERCEL: "https://relay-deployer-santo-santonijaya8-4344s-projects.vercel.app,https://relay-santo-02-santonijaya8-4344s-projects.vercel.app,https://relay-santo-03-santonijaya8-4344s-projects.vercel.app,https://relay-deployer-auah-auah-s-projects.vercel.app,https://relay-auah-02-auah-s-projects.vercel.app,https://relay-deployer-sengkyou-goodboyguds-projects.vercel.app,https://relay-deployer-zidan.vercel.app,https://relay-deployer-znaekz.vercel.app,https://relay-deployer-zidan-02.vercel.app,https://relay-deployer-mahavpn.vercel.app,https://relay-us-auah.vercel.app,https://relay-us-sengkyou.vercel.app,https://relay-us-santony.vercel.app,https://relay-us-zidan.vercel.app",
        RELAY_CLOUDFLARE: "https://aurora8456-inbox-nullpter-web-id.a8310d927dc.workers.dev,https://caleb05-student-nullpter-web-id.a746906ca1e.workers.dev,https://salma31-teach-nullpter-web-id.aebd1a703c8.workers.dev"
      }
    }
  ]
};
