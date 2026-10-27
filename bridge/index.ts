import { initSpacemoltClients, listOwnedPlayers, listOwnedPlayersByKey, getConnectedAccount, removeConnectedAccount } from "./libClient.ts";
import { SpacemoltClient } from "@spacemolt/lib";

const PORT = Number(process.env.PORT || 8787);

// Send the current account state as an account.status event to the Go client.
// Reads account.state directly (the cached GameState) so we always ship fresh
// data — useful both for initial seeding after connect and for onStateChange
// push updates.
function sendAccountStatus(ws: any, account: any) {
  const st = account.state as any;
  if (!st) {
    console.log(
      `[bridge] account.status id=${account.id} username=${account.username} state=MISSING`,
    );
    return;
  }
  const player = st.player ?? {};
  const ship = st.ship ?? {};
  console.log(
    `[bridge] account.status id=${account.id} username=${account.username} credits=${player.credits} fuel=${ship.fuel} hull=${ship.hull} system=${st.location?.system_id} poi=${st.location?.poi_id} ship=${ship.name} cargo=${JSON.stringify(st.cargo)}`,
  );
  ws.send(
    JSON.stringify({
      jsonrpc: "2.0",
      method: "account.status",
      params: {
        id: account.id,
        username: account.username,
        credits: player.credits,
        fuel: ship.fuel,
        maxFuel: ship.max_fuel,
        hull: ship.hull,
        maxHull: ship.max_hull,
        shield: ship.shield,
        maxShield: ship.max_shield,
        cargo: st.cargo,
        isCloaked: ship.is_cloaked,
        system: st.location?.system_id,
        poi: st.location?.poi_id,
        location: st.location?.id || st.location?.name,
        ship: ship.name || ship.id,
      },
    }),
  );
}

type RpcReq = { jsonrpc: string; id?: number; method: string; params?: any };

const server = Bun.serve({
  port: PORT,
  fetch(req, srv) {
    if (srv.upgrade(req)) return;
    return new Response("bridge ok");
  },
  websocket: {
    open(ws) {
      (ws as any)._subs = [];
    },
    async message(ws, raw) {
      let req: RpcReq;
      try { req = JSON.parse(String(raw)); } catch { return; }
      const reply = (result?: any, error?: any) => {
        ws.send(JSON.stringify({ jsonrpc: "2.0", id: req.id, result, error }));
      };
      try {
        switch (req.method) {
          case "initClients": {
            initSpacemoltClients(req.params?.keys ?? []);
            reply({ ok: true });
            break;
          }
          case "listOwnedPlayers": {
            const players = await listOwnedPlayers();
            reply(players);
            break;
          }
          case "listOwnedPlayersByKey": {
            const groups = await listOwnedPlayersByKey();
            reply(groups);
            break;
          }
          case "connectOwned": {
            const { filter, botIds, ids } = req.params || {};
            const playerIds = botIds ?? ids;
            const { connectOwnedAccounts } = await import("./libClient.ts");
            console.log(
              `[bridge] connectOwned playerIds=${JSON.stringify(playerIds)}`,
            );
            const accounts = await connectOwnedAccounts(
              (p: any) => (playerIds ? playerIds.includes(p.username || p.id) : true) && (filter ? filter(p) : true),
              (account: any) => {
                // push connected event
                console.log(
                  `[bridge] account.connected id=${account.id} username=${account.username}`,
                );
                ws.send(JSON.stringify({ jsonrpc: "2.0", method: "account.connected", params: { id: account.id, username: account.username, account: { id: account.id, username: account.username } } }));
                // listen disconnect
                account.onDisconnected?.((err: any) => {
                  ws.send(JSON.stringify({ jsonrpc: "2.0", method: "account.disconnected", params: { id: account.id, reason: String(err) } }));
                });
                // forward live state changes to Go dashboard
                account.onStateChange?.((_changed: any[]) => {
                  sendAccountStatus(ws, account);
                });
                // Seed state and send initial status after connect completes.
                // Await refresh() first so account.state is populated before
                // we read player.credits / ship.fuel etc. from it.
                account.refresh().then(() => sendAccountStatus(ws, account)).catch(() => sendAccountStatus(ws, account));
              },
            );
            console.log(
              `[bridge] connectOwned done accounts=${accounts.map((a:any)=>a.id).join(",")}`,
            );
            reply(accounts.map((a: any) => ({ id: a.id, username: a.username })));
            break;
          }
          case "getAccount": {
            const acc = getConnectedAccount(req.params?.id);
            if (!acc) { reply(null, { code: -1, message: "not found" }); break; }
            reply({ id: acc.id, location: acc.location, credits: acc.credits });
            break;
          }
          case "exec": {
            const acc = getConnectedAccount(req.params?.id);
            console.log(
              `[bridge] exec id=${req.params?.id} command=${req.params?.command} accFound=${!!acc}`,
            );
            if (!acc) {
              reply(null, { code: -1, message: "account not found" });
              break;
            }
            const { command, params } = req.params || {};
            // command like "get_status" or tool/action? Support exec with command string
            let result;
            if (command && typeof acc.send === "function") {
              const parts = String(command).split(".");
              if (parts.length === 2) {
                result = await acc.send(parts[0], parts[1], params || {});
              } else {
                result = await acc.query("spacemolt", command, params || {});
              }
            }
            console.log(
              `[bridge] exec id=${req.params?.id} command=${req.params?.command} done result=${JSON.stringify(result)?.slice(0, 300)}`,
            );
            reply(result);
            break;
          }
          case "removeAccount": {
            removeConnectedAccount(req.params?.id);
            reply({ ok: true });
            break;
          }
          case "closeAccount": {
            const acc = getConnectedAccount(req.params?.id);
            if (acc) acc.close();
            removeConnectedAccount(req.params?.id);
            reply({ ok: true });
            break;
          }
          default:
            reply(null, { code: -2, message: "unknown method" });
        }
      } catch (e: any) {
        reply(null, { code: -3, message: String(e?.message || e) });
      }
    },
  },
});

console.log(`bridge listening on ${PORT}`);
