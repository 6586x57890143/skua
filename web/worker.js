// skua.melting.lol: the short invite. Discord's bare install link asks for the
// app's Default Install Settings, which tools/setup writes from invitePerms, so
// this never has to change when a module needs a new permission.
export default {
	fetch(_req, env) {
		return Response.redirect(`https://discord.com/oauth2/authorize?client_id=${env.APP_ID}`, 302);
	},
};
