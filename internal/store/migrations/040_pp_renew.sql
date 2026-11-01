-- Birds who asked the yard to renew their livery's lease on its own, at
-- the turn of its last day, while the purse can pay.
create table pp_renew (
	guild_id bigint not null,
	user_id  bigint not null,
	primary key (guild_id, user_id)
);
