-- How each member wants their reminders: where one goes when they don't
-- say (dm, or here: the channel it was set in), the three snooze buttons
-- in minutes (-1 is tomorrow at the same time), and quiet hours as
-- minutes of the day in their zone, -1 when off. A dm due in them waits
-- until they end.
alter table remind_user
	add column if not exists deliver text not null default 'dm',
	add column if not exists snooze int[] not null default '{10,60,-1}',
	add column if not exists quiet_from smallint not null default -1,
	add column if not exists quiet_to smallint not null default -1;
