-- 0002_seed: example people, groups and a few weeks of expenses, dated
-- relative to the day the migration runs so the app always looks lived in.

INSERT INTO people (id, name, email, color) VALUES
	('sam',    'Sam Rivera',   'sam@example.com',    'teal'),
	('priya',  'Priya Shah',   'priya@example.com',  'violet'),
	('marcus', 'Marcus Webb',  'marcus@example.com', 'orange'),
	('lena',   'Lena Novak',   'lena@example.com',   'pink'),
	('diego',  'Diego Alves',  'diego@example.com',  'sky'),
	('aiko',   'Aiko Tanaka',  'aiko@example.com',   'amber');

INSERT INTO groups (id, name, emoji, color, created_by, created_at) VALUES
	(1, 'Flat 4B',       '🏠', 'teal',   'sam',   now() - interval '90 days'),
	(2, 'Lisbon trip',   '🌊', 'sky',    'lena',  now() - interval '30 days'),
	(3, 'Climbing club', '⛰️', 'orange', 'diego', now() - interval '60 days');
SELECT setval('groups_id_seq', 3);

INSERT INTO members (group_id, person_id) VALUES
	(1, 'sam'), (1, 'priya'), (1, 'marcus'),
	(2, 'sam'), (2, 'lena'), (2, 'diego'), (2, 'aiko'), (2, 'priya'),
	(3, 'sam'), (3, 'diego'), (3, 'marcus');

-- add_expense splits an expense equally among the group's members (or
-- the listed ones), giving any leftover cents to the first.
CREATE FUNCTION pg_temp.add_expense(g bigint, descr text, cat text, cents bigint, payer text, days_ago int, among text[] DEFAULT NULL)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE
	eid bigint;
	who text[];
	n int;
	each bigint;
	i int;
BEGIN
	who := coalesce(among, ARRAY(SELECT person_id FROM members WHERE group_id = g ORDER BY person_id));
	n := array_length(who, 1);
	each := cents / n;
	INSERT INTO expenses (group_id, description, category, amount_cents, paid_by, spent_on, created_by, created_at)
	VALUES (g, descr, cat, cents, payer, current_date - days_ago, payer, now() - make_interval(days => days_ago))
	RETURNING id INTO eid;
	FOR i IN 1..n LOOP
		INSERT INTO shares (expense_id, person_id, amount_cents)
		VALUES (eid, who[i], each + CASE WHEN i = 1 THEN cents - each * n ELSE 0 END);
	END LOOP;
END $$;

SELECT pg_temp.add_expense(1, 'October rent',            'rent',      240000, 'sam',    10);
SELECT pg_temp.add_expense(1, 'Electricity',             'utilities',  8642,  'priya',  8);
SELECT pg_temp.add_expense(1, 'Broadband',               'utilities',  4500,  'marcus', 7);
SELECT pg_temp.add_expense(1, 'Big shop at Lidl',        'groceries',  6237,  'priya',  5);
SELECT pg_temp.add_expense(1, 'Cleaning supplies',       'household',  1849,  'marcus', 4);
SELECT pg_temp.add_expense(1, 'Pizza night',             'dining',     4260,  'sam',    2, ARRAY['sam','priya']);
SELECT pg_temp.add_expense(1, 'Farmers market',          'groceries',  3115,  'sam',    1);
SELECT pg_temp.add_expense(1, 'September rent',          'rent',      240000, 'sam',    40);
SELECT pg_temp.add_expense(1, 'Water bill',              'utilities',  3920,  'marcus', 35);
SELECT pg_temp.add_expense(1, 'New kettle',              'household',  3499,  'priya',  33);

SELECT pg_temp.add_expense(2, 'Flights to LIS',          'travel',    92450, 'lena',   21);
SELECT pg_temp.add_expense(2, 'Alfama apartment, 4 nights', 'lodging', 68000, 'diego', 20);
SELECT pg_temp.add_expense(2, 'Pastéis de Belém',        'dining',     1860, 'aiko',   14);
SELECT pg_temp.add_expense(2, 'Tram 28 day passes',      'transport',  3200, 'sam',    14);
SELECT pg_temp.add_expense(2, 'Seafood dinner, Cervejaria Ramiro', 'dining', 21475, 'priya', 13);
SELECT pg_temp.add_expense(2, 'Sintra day trip',         'travel',    11500, 'lena',   12);
SELECT pg_temp.add_expense(2, 'Fado tickets',            'entertainment', 9000, 'sam', 12, ARRAY['sam','lena','aiko']);
SELECT pg_temp.add_expense(2, 'Groceries for the flat',  'groceries',  4630, 'diego',  11);
SELECT pg_temp.add_expense(2, 'Taxi to airport',         'transport',  3850, 'aiko',   10);

SELECT pg_temp.add_expense(3, 'Bouldering passes (10x)', 'sport',     18000, 'diego',  25);
SELECT pg_temp.add_expense(3, 'Chalk and tape',          'sport',      2475, 'marcus', 18);
SELECT pg_temp.add_expense(3, 'Post-session burritos',   'dining',     3690, 'sam',     6);
SELECT pg_temp.add_expense(3, 'Rope (shared)',           'sport',     14999, 'diego',   3);

INSERT INTO settlements (group_id, from_person, to_person, amount_cents, created_by, created_at) VALUES
	(1, 'marcus', 'sam', 80000, 'marcus', now() - interval '30 days'),
	(1, 'priya',  'sam', 76000, 'priya',  now() - interval '29 days'),
	(2, 'aiko',   'lena', 15000, 'aiko',  now() - interval '9 days');

INSERT INTO recurring (group_id, description, category, amount_cents, paid_by, day_of_month, last_posted) VALUES
	(1, 'Rent',      'rent',      240000, 'sam',    1, date_trunc('month', current_date)::date),
	(1, 'Broadband', 'utilities',   4500, 'marcus', 15, NULL),
	(1, 'Cleaner',   'household',   6000, 'priya',   5, NULL);

INSERT INTO notifications (person_id, group_id, body, created_at) VALUES
	('sam', 2, 'Aiko settled up with Lena: $150.00', now() - interval '9 days'),
	('sam', 1, 'Priya added "Big shop at Lidl" to Flat 4B', now() - interval '5 days');
