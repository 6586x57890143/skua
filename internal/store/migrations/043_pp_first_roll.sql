-- The first roll: each member's place on the level ladder as the
-- preseason left it, for the first ten, 0 for none. Taken once, at the
-- turn into season 1.
alter table pp_member add column first_roll bigint not null default 0;

-- Season 1 turned before the roll shipped, so it's taken here from what
-- the preseason left: a member's total less what this season has added
-- (total and season_pp only ever grow together), ranked as Places ranks,
-- a tie sharing its place.
update pp_member m set first_roll = r.place
from (
	select guild_id, user_id, rank() over (partition by guild_id order by pre desc) as place
	from (
		select guild_id, user_id, total - case when season >= 1 then season_pp else 0 end as pre
		from pp_member
	) p
	where pre > 0
) r
where m.guild_id = r.guild_id and m.user_id = r.user_id and r.place <= 10;
