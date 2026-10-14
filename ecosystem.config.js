module.exports = {
  apps: [
    {
      name: "relay-bridge",
      script: "/home/agentuser/relay-bridge/relay-bridge",
      args: "-http 127.0.0.1:8880 -socks 127.0.0.1:1180 -mitm",
      exec_mode: "fork",
      instances: 1,
      autorestart: true,
      env: {
        RELAY_TYPE: "vercel",
        RELAY_VERCEL: "https://relay-deployer-santo-santonijaya8-4344s-projects.vercel.app,https://relay-santo-02-santonijaya8-4344s-projects.vercel.app,https://relay-santo-03-santonijaya8-4344s-projects.vercel.app,https://relay-deployer-auah-auah-s-projects.vercel.app,https://relay-auah-02-auah-s-projects.vercel.app,https://relay-auah-03-auah-s-projects.vercel.app,https://relay-deployer-sengkyou-goodboyguds-projects.vercel.app,https://relay-sengkyou-02-goodboyguds-projects.vercel.app,https://relay-sengkyou-03-goodboyguds-projects.vercel.app",
        POOL_MODE: "round_robin"
      }
    }
  ]
};
