-- The north sea company's sealed letters: the codes a server's admins
-- loaded, each going to the first bird to earn a letter (winner 0 until
-- then), the unix second it went and whether its seal is broken. A bird
-- holds one letter at most.
create table pp_letter (
	guild_id bigint  not null,
	code     text    not null,
	winner   bigint  not null default 0,
	won_at   bigint  not null default 0,
	opened   boolean not null default false,
	primary key (guild_id, code)
);
create unique index pp_letter_winner on pp_letter (guild_id, winner) where winner <> 0;
