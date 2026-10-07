// skua.lol: the short invite. Discord's bare install link asks for the
// app's Default Install Settings, which skua writes at boot from its modules, so
// this never has to change when a module needs a new permission.
export default {
	fetch(_req, env) {
		return Response.redirect(`https://discord.com/oauth2/authorize?client_id=${env.APP_ID}`, 302);
	},
};
